#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""把会议纪要一键改写为国家开放大学会议纪要格式。

用法：
    python apply_format.py 纪要.docx
    python apply_format.py 纪要.docx --fill fill.json --output 输出.docx
    python apply_format.py 纪要.docx --dry-run

默认写 <原名>_国开会议纪要格式.docx，**不覆盖原文件**。

脚本全程非交互。缺内容的项从 --fill 的 JSON 里取；没给的写
【待补充：X】占位并在摘要里列出 —— 脚本绝不自行编写内容。

fill.json 可用键：
    meeting_name  红头第二行的会议名（如「"一平台"项目」，脚本补「会议纪要」）
    title         纪要标题
    time / place / host / attendees / recorder / topic   信息栏各项
    lead          帽子段
"""
from __future__ import print_function

import argparse
import io
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import mj_docx as X          # noqa: E402
import mj_spec as S          # noqa: E402

PLACEHOLDER = "【待补充：%s】"
OUTPUT_SUFFIX = "_国开会议纪要格式"


def placeholder_for(key):
    f = S.INFO_BY_KEY.get(key)
    label = f["label"] if f else key
    return PLACEHOLDER % label


# ---------------- 各角色的排版 ----------------
def format_redhead(p):
    spec = S.REDHEAD
    X.set_paragraph_spacing(p, X.pt_to_twips(spec["line_pt"]), "exact",
                            after=X.pt_to_twips(spec["after_pt"]))
    X.set_alignment(p, spec["align"])
    X.clear_first_line_indent(p)
    X.set_paragraph_runs_font(p, spec["font"], spec["size_pt"],
                              spec["bold"], spec["color"])


def format_title(p):
    spec = S.TITLE
    X.set_paragraph_spacing(p, X.pt_to_twips(spec["line_pt"]), "exact")
    X.set_alignment(p, spec["align"])
    X.clear_first_line_indent(p)
    X.set_paragraph_runs_font(p, spec["font"], spec["size_pt"], spec["bold"])


def format_info(p, field, value_text=None):
    """信息栏：标签黑体 + 值仿宋，标签补齐到 4 汉字宽。"""
    X.set_paragraph_spacing(p, X.pt_to_twips(S.LINE_BODY_PT), "exact")
    X.set_alignment(p, "left")
    X.clear_first_line_indent(p)
    label = X.pad_label(field["label"], S.INFO_LABEL_WIDTH_HAN) + "："
    if value_text is None:
        # 从原段落取值：第一个冒号之后的部分
        raw = X.paragraph_text(p)
        parts = raw.split("：", 1) if "：" in raw else raw.split(":", 1)
        value_text = parts[1].strip() if len(parts) > 1 else ""
    X.set_label_value_runs(p, label, value_text, S.INFO_LABEL, S.INFO_VALUE)


def format_dept(p):
    """出席人员下方的部门行：整行仿宋，不缩进。"""
    X.set_paragraph_spacing(p, X.pt_to_twips(S.LINE_BODY_PT), "exact")
    X.set_alignment(p, "left")
    X.clear_first_line_indent(p)
    X.set_paragraph_runs_font(p, S.INFO_VALUE["font"], S.INFO_VALUE["size_pt"],
                              S.INFO_VALUE["bold"])


def format_text_role(p, role, text=None, h3_bold=None):
    """正文/帽子段/各级标题：同缩进体例，字体按级别。

    标题与说明合排的段落（「（一）登录认证演示。演示了……」）拆成两段 run：
    标题部分用该级标题字体并加粗，说明部分保持正文字体。整段刷标题字体
    会让一大段说明文字都变楷体，不是模板的意思。

    正文中的 **加粗** 标记会被识别并生成加粗 run。

    h3_bold: 仅对 h3 角色有效。True=加粗（会议要点下），False=不加粗
    （下一步工作计划下），None=按原 spec 决定。
    """
    spec = S.spec_for(role)
    # h3 加粗规则区分：会议要点下加粗，下一步工作下不加粗
    if role == "h3" and h3_bold is not None:
        spec = dict(spec)
        spec["bold"] = h3_bold
    X.set_paragraph_spacing(p, X.pt_to_twips(spec["line_pt"]), "exact")
    X.set_alignment(p, spec["align"])
    X.set_first_line_indent_chars(p, spec["indent_chars"],
                                  X.pt_to_half_points(spec["size_pt"]))

    if S.heading_level(role):
        if text is None:
            text = X.paragraph_text(p)
        parts = S.split_inline_heading(text)
        if parts:
            head_spec = dict(spec)
            head_spec["bold"] = True     # 合排时靠加粗把标题拎出来
            X.set_two_part_runs(p, parts[0], parts[1], head_spec, S.BODY)
            return

    # 正文 / 帽子段 / 纯标题段：支持 **加粗** 标记
    if text is None:
        text = X.paragraph_text(p)
    if "**" in text:
        X.set_bold_marked_runs(p, text, spec)
    else:
        X.set_paragraph_runs_font(p, spec["font"], spec["size_pt"], spec["bold"])


def format_photo(p):
    """照片段：行距交给 Word 自适应，不缩进，居中。

    照片段**不能**按正文排 —— 固定行距（lineRule="exact"）是硬裁剪，
    11.92cm 的 inline 照片只剩 28 磅（0.99cm），裁掉 92%，就露出顶上一条。
    首行缩进也要清：照片宽 15.89cm，文本栏 15.92cm，加 2 字符缩进
    （1.13cm）会溢出 1.10cm。
    """
    X.set_paragraph_spacing_auto(p)
    X.set_alignment(p, S.PHOTO["align"])
    X.clear_first_line_indent(p)


def format_blank(p):
    X.set_paragraph_spacing(p, X.pt_to_twips(S.LINE_BODY_PT), "exact")


# ---------------- 插入缺失段落 ----------------
def _insert_after(body, ref_idx, p):
    """把 p 插到 body 里第 ref_idx 个直属段落之后。ref_idx=-1 表示插到最前。"""
    paras = body.findall(X.qn("w:p"))
    if ref_idx < 0 or not paras:
        # 插到 body 最前（body 里 sectPr 在最后，不受影响）
        body.insert(0, p)
        return
    ref = paras[min(ref_idx, len(paras) - 1)]
    body.insert(list(body).index(ref) + 1, p)


def add_redline(p, summary, expect_text=None):
    """在红头最后一行下方画红线（段落下边框实现）。

    expect_text 传红头末行的文字做兜底核对 —— 之前有过一个 bug：调用方
    拿插入新段落之前算的 roles 索引取段落，索引已经后移，线画到了标题上。
    锚点必须是当下真正的红头末行。
    """
    if expect_text is not None:
        actual = X.paragraph_text(p).strip()
        if actual != expect_text.strip():
            raise AssertionError(
                "红线锚点不对：期望红头末行 %r，实际拿到 %r" % (expect_text, actual))
    X.set_redline_border(p, S.REDLINE_COLOR, S.REDLINE_BORDER_SZ,
                         S.REDLINE_BORDER_SPACE)
    summary["added"].append("红头下方的 3 磅红线")


def _redhead_second_line(fill, summary):
    """红头第二行的文字：会议名 + 「会议纪要」。没给会议名就留占位。"""
    name = (fill.get("meeting_name") or "").strip()
    if name:
        return name if name.endswith(S.REDHEAD_SUFFIX) else name + S.REDHEAD_SUFFIX
    summary["placeholders"].append("红头会议名称")
    return PLACEHOLDER % "会议名称" + S.REDHEAD_SUFFIX


def add_redhead(body, fill, summary):
    """补两行红头。会议名从 fill['meeting_name'] 取，没给则留占位。"""
    second = _redhead_second_line(fill, summary)
    last = None
    for k, text in enumerate((S.REDHEAD_ORG, second)):
        p = X.new_paragraph()
        X.set_paragraph_text(p, text, dict(S.REDHEAD))
        _insert_after(body, k - 1, p)
        format_redhead(p)
        last = p
    summary["added"].append("红头 2 行")
    if last is not None:
        add_redline(last, summary, second)


def add_redhead_second_line(body, first_idx, fill, summary):
    """红头只有「国家开放大学」一行时，补出第二行（会议名）。

    原来判的是 `if "redhead" not in roles` —— 有一行就整段跳过，
    第二行永远补不上，跟校验器 B3「可自动修」的承诺不符。
    """
    second = _redhead_second_line(fill, summary)
    p = X.new_paragraph()
    X.set_paragraph_text(p, second, dict(S.REDHEAD))
    _insert_after(body, first_idx, p)
    format_redhead(p)
    summary["added"].append("红头第二行「%s」" % second)
    # 线原来可能画在第一行上，挪到新的末行
    paras = X.top_level_paragraphs(body)
    if first_idx < len(paras):
        X.clear_redline_border(paras[first_idx])
    add_redline(p, summary, second)


def add_title(body, after_idx, fill, summary):
    text = (fill.get("title") or "").strip()
    if not text:
        text = PLACEHOLDER % "纪要标题"
        summary["placeholders"].append("纪要标题")
    p = X.new_paragraph()
    X.set_paragraph_text(p, text, dict(S.TITLE))
    _insert_after(body, after_idx, p)
    format_title(p)
    summary["added"].append("标题")


def add_info(body, after_idx, field, fill, summary):
    value = (fill.get(field["key"]) or "").strip()
    if not value:
        value = placeholder_for(field["key"])
        summary["placeholders"].append(field["label"])
    p = X.new_paragraph()
    _insert_after(body, after_idx, p)
    format_info(p, field, value)
    summary["added"].append("信息栏「%s」" % field["label"])


def add_lead(body, after_idx, fill, summary):
    text = (fill.get("lead") or "").strip()
    if not text:
        text = PLACEHOLDER % "帽子段（会议背景概述）"
        summary["placeholders"].append("帽子段")
    p = X.new_paragraph()
    X.set_paragraph_text(p, text, dict(S.BODY))
    _insert_after(body, after_idx, p)
    format_text_role(p, "lead")
    summary["added"].append("帽子段")


# ---------------- 主流程 ----------------
def apply_format(input_path, output_path=None, fill=None, dry_run=False):
    fill = dict(fill or {})
    summary = {"input": input_path, "added": [], "placeholders": [],
               "manual": [], "counts": {}}

    root, body, names, data, infos = X.load_document(input_path)

    # ---- 第一遍：按现有段落排版 ----
    paras = X.top_level_paragraphs(body)
    texts = [X.paragraph_text(p) for p in paras]
    roles = S.classify_all(texts, paras)

    counts = {}
    for i, (p, role) in enumerate(zip(paras, roles)):
        counts[role] = counts.get(role, 0) + 1
        if role == "redhead":
            format_redhead(p)
        elif role == "title":
            format_title(p)
        elif role == "info":
            format_info(p, S.match_info_label(texts[i]))
        elif role == "dept":
            format_dept(p)
        elif role in ("lead", "body", "h1", "h2", "h3", "h4"):
            format_text_role(p, role, texts[i])
        elif role == "photo":
            format_photo(p)
        elif role == "blank":
            format_blank(p)
    summary["counts"] = counts

    # ---- 第二遍：补缺失的结构 ----
    if "redhead" not in roles:
        add_redhead(body, fill, summary)
        # 重新定位（插了段落，索引变了）
        paras = X.top_level_paragraphs(body)
        texts = [X.paragraph_text(p) for p in paras]
        roles = S.classify_all(texts, paras)
    elif roles.count("redhead") == 1:
        # 只有「国家开放大学」一行，补第二行
        add_redhead_second_line(body, roles.index("redhead"), fill, summary)
        paras = X.top_level_paragraphs(body)
        texts = [X.paragraph_text(p) for p in paras]
        roles = S.classify_all(texts, paras)

    if "title" not in roles:
        last_redhead = max([i for i, r in enumerate(roles) if r == "redhead"] or [-1])
        add_title(body, last_redhead, fill, summary)
        paras = X.top_level_paragraphs(body)
        texts = [X.paragraph_text(p) for p in paras]
        roles = S.classify_all(texts, paras)

    # 信息栏缺项：按模板顺序补在合适位置
    present = {}
    for i, r in enumerate(roles):
        if r == "info":
            f = S.match_info_label(texts[i])
            if f:
                present[f["key"]] = i
    for pos, field in enumerate(S.INFO_FIELDS):
        if field["key"] in present:
            continue
        # 插到前一个已存在的信息栏项之后；都没有就插到标题之后
        anchor = -1
        for prev in reversed(S.INFO_FIELDS[:pos]):
            if prev["key"] in present:
                anchor = present[prev["key"]]
                break
        if anchor < 0:
            title_idx = [i for i, r in enumerate(roles) if r == "title"]
            anchor = max(title_idx) if title_idx else \
                max([i for i, r in enumerate(roles) if r == "redhead"] or [-1])
        add_info(body, anchor, field, fill, summary)
        paras = X.top_level_paragraphs(body)
        texts = [X.paragraph_text(p) for p in paras]
        roles = S.classify_all(texts, paras)
        present = {}
        for i, r in enumerate(roles):
            if r == "info":
                f = S.match_info_label(texts[i])
                if f:
                    present[f["key"]] = i

    # 帽子段
    if "lead" not in roles and "content" in present:
        add_lead(body, present["content"], fill, summary)
        paras = X.top_level_paragraphs(body)
        texts = [X.paragraph_text(p) for p in paras]
        roles = S.classify_all(texts, paras)

    # ---- 页面设置 ----
    mar = X.cm_to_twips(S.PAGE_MARGIN_CM)
    for sect in X.iter_sectPr(root):
        X.set_page_size(sect, S.A4_W, S.A4_H)
        X.set_page_margins(sect, mar, mar, mar, mar,
                           S.PAGE_HEADER_TWIPS, S.PAGE_FOOTER_TWIPS)

    # ---- 红线：红头有了但线没有时补上（原件自带红头的情况）----
    # 这里必须重新识别一次。前面补标题/信息栏/帽子段都插了段落，body 里的
    # 索引已经后移，拿上面那份 roles 的索引会取到错的段落 —— 之前的 bug
    # 就是这么把线画到标题下面的。
    paras = X.top_level_paragraphs(body)
    texts = [X.paragraph_text(p) for p in paras]
    roles = S.classify_all(texts, paras)
    rh = [i for i, r in enumerate(roles) if r == "redhead"]
    rl = X.find_redline(body, S.REDLINE_COLOR)
    if rh:
        want = max(rh)
        if rl is None:
            add_redline(paras[want], summary, texts[want])
        elif rl[0] != want:
            # 线在别的段落上（存量文件里有画到标题下面的）。只清下边框实现的，
            # 模板那条浮动形状本来就锚在红头第二段、位置是对的，不动。
            moved = False
            for i, p in enumerate(paras):
                if i != want and X.paragraph_has_redline_border(p, S.REDLINE_COLOR):
                    X.clear_redline_border(p)
                    moved = True
            if moved:
                X.set_redline_border(paras[want], S.REDLINE_COLOR,
                                     S.REDLINE_BORDER_SZ, S.REDLINE_BORDER_SPACE)
                summary["added"].append("红线位置纠正到红头末行")

    # ---- 修不了的，如实列出 ----
    if "photo" not in roles:
        summary["manual"].append(
            "文末缺会议照片 —— 脚本无法生成图片，需手动插入")

    summary["roles"] = [{"index": i, "role": r, "text": texts[i].strip()[:40]}
                        for i, r in enumerate(roles) if r != "blank"]

    if dry_run:
        summary["output"] = None
        summary["dry_run"] = True
        return summary

    # ---- 写文件 ----
    if not output_path:
        base, ext = os.path.splitext(input_path)
        output_path = base + OUTPUT_SUFFIX + ext
    if os.path.abspath(output_path) == os.path.abspath(input_path):
        raise ValueError("输出路径不能与输入相同（本脚本不覆盖原文件）")

    data[X.DOC_PART] = X.serialize(root)
    X.write_parts(output_path, names, data, infos)
    warnings = X.validate_docx(output_path)
    if warnings:
        raise RuntimeError("产出文件校验失败：%s" % warnings)

    summary["output"] = output_path
    return summary


def print_summary(s, out):
    out.write("国开会议纪要格式改写\n")
    out.write("=" * 60 + "\n\n")
    out.write("输入：%s\n" % s["input"])
    if s.get("dry_run"):
        out.write("（dry-run，未写文件）\n")
    else:
        out.write("输出：%s\n" % s["output"])
    out.write("\n段落识别：%s\n" % ", ".join(
        "%s×%d" % (k, v) for k, v in sorted(s["counts"].items())))
    if s["added"]:
        out.write("\n已补结构：\n")
        for a in s["added"]:
            out.write("  + %s\n" % a)
    if s["placeholders"]:
        out.write("\n以下内容未提供，已写入占位，需你补齐后再发出：\n")
        for p in s["placeholders"]:
            out.write("  ! 【待补充：%s】\n" % p)
    if s["manual"]:
        out.write("\n一键改写修不了的（需人工）：\n")
        for m in s["manual"]:
            out.write("  * %s\n" % m)
    out.write("\n提示：%s 未安装时 Word/WPS 会替换显示，文件内字体名是正确的。\n"
              % "、".join(S.FONTS_MAY_MISSING))


def main():
    ap = argparse.ArgumentParser(description="改写为国家开放大学会议纪要格式")
    ap.add_argument("input", help="待改写的 .docx")
    ap.add_argument("--output", help="输出路径（默认 <原名>%s.docx）" % OUTPUT_SUFFIX)
    ap.add_argument("--fill", help="补充内容的 JSON 文件")
    ap.add_argument("--dry-run", action="store_true", help="只识别不写文件")
    ap.add_argument("--json", action="store_true", help="输出 JSON 摘要")
    args = ap.parse_args()

    if not os.path.isfile(args.input):
        print("ERROR: 文件不存在：%s" % args.input, file=sys.stderr)
        sys.exit(2)
    if not args.input.lower().endswith(".docx"):
        print("ERROR: 只支持 .docx（.doc 请先转换）", file=sys.stderr)
        sys.exit(2)

    fill = {}
    if args.fill:
        if not os.path.isfile(args.fill):
            print("ERROR: --fill 文件不存在：%s" % args.fill, file=sys.stderr)
            sys.exit(2)
        with io.open(args.fill, encoding="utf-8") as fh:
            fill = json.load(fh)

    try:
        s = apply_format(args.input, args.output, fill, args.dry_run)
    except Exception as e:
        print("ERROR: %s" % e, file=sys.stderr)
        sys.exit(1)

    out = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", newline="")
    if args.json:
        out.write(json.dumps(s, ensure_ascii=False, indent=2) + "\n")
    else:
        print_summary(s, out)
    out.flush()


if __name__ == "__main__":
    main()
