#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""从 Markdown / 纯文本生成合规的国开会议纪要 docx。

用法：
    python build_from_text.py 纪要.md --fill fill.json -o 输出.docx

拿 assets/国家开放大学会议纪要模板.docx 当骨架 —— 清空正文段落但保留
sectPr、styles、红头区（含红线），再按 md 结构逐段插入。这样红线和
样式定义都不用重新造。

md 约定：
    # 标题            -> 纪要标题
    ## 一级            -> 一、（脚本自动编号）
    ### 二级           -> （一）
    #### 三级          -> 1.
    时间：…            -> 信息栏（顶部连续的「标签：值」行）
    其余段落           -> 正文（第一段为帽子段）

信息栏也可以走 --fill；md 里写了的优先，缺的从 fill 取，都没有留占位。
"""
from __future__ import print_function

import argparse
import io
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import mj_docx as X          # noqa: E402
import mj_spec as S          # noqa: E402
import apply_format as A     # noqa: E402

TEMPLATE = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                        "..", "assets", "国家开放大学会议纪要模板.docx")

CN_NUM = "一二三四五六七八九十"


def cn_number(n):
    """1 -> 一，11 -> 十一（纪要用不到更大的）。"""
    if n <= 10:
        return CN_NUM[n - 1]
    if n < 20:
        return "十" + (CN_NUM[n - 11] if n > 10 else "")
    return str(n)


def parse_text(path):
    """把 md/txt 解析成 {'title':…, 'info':{…}, 'blocks':[(role,text),…]}。"""
    with io.open(path, encoding="utf-8") as fh:
        lines = fh.read().splitlines()

    title = None
    info = {}
    blocks = []
    h1n = h2n = h3n = 0
    in_head = True   # 顶部信息栏区

    for raw in lines:
        line = raw.rstrip()
        if not line.strip():
            continue
        m = re.match(r"^(#{1,4})\s+(.*)$", line)
        if m:
            level, text = len(m.group(1)), m.group(2).strip()
            if level == 1 and title is None:
                title = text
                continue      # 标题不结束信息栏区，信息栏通常写在标题之后
            in_head = False
            if level <= 2:
                h1n += 1
                h2n = h3n = 0
                blocks.append(("h1", "%s、%s" % (cn_number(h1n), text)))
            elif level == 3:
                h2n += 1
                h3n = 0
                blocks.append(("h2", "（%s）%s" % (cn_number(h2n), text)))
            else:
                h3n += 1
                blocks.append(("h3", "%d. %s" % (h3n, text)))
            continue

        # 顶部的「标签：值」当信息栏
        if in_head:
            f = S.match_info_label(line)
            if f:
                val = re.split(r"[：:]", line.strip(), 1)[1].strip()
                info[f["key"]] = val
                continue
            # 出席人员下方的部门行
            if info.get("attendees") is not None and S.looks_like_dept_line(line):
                blocks.append(("dept", line.strip()))
                continue

        in_head = False
        # 去掉 md 列表符号，纪要正文不用 bullet
        text = re.sub(r"^\s*[-*+]\s+", "", line).strip()
        # md 里手写了「（一）」「1.」这类编号时，按对应级别排 —— 不然这里当
        # body 排（仿宋不加粗），校验器按 h2 判，产出自己就报 F1。
        blocks.append((S.heading_role(text) or "body", text))

    # 第一段正文作帽子段
    for i, (role, text) in enumerate(blocks):
        if role == "body":
            blocks[i] = ("lead", text)
            break
    return {"title": title, "info": info, "blocks": blocks}


def _clear_body_paragraphs(body, keep_from_redhead=True):
    """清空正文段落，保留红头两段（含红线锚点）与 sectPr。

    返回保留下来的段落数。
    """
    paras = body.findall(X.qn("w:p"))
    texts = [X.paragraph_text(p) for p in paras]
    roles = S.classify_all(texts, paras)
    keep = set()
    if keep_from_redhead:
        keep = {i for i, r in enumerate(roles) if r == "redhead"}
        # 红线锚在哪段就必须留那段
        rl = X.find_redline(body, S.REDLINE_COLOR)
        if rl:
            keep.add(rl[0])
    for i, p in enumerate(paras):
        if i not in keep:
            body.remove(p)
    return len(keep)


def build(text_path, output_path, fill=None):
    fill = dict(fill or {})
    parsed = parse_text(text_path)
    summary = {"input": text_path, "added": [], "placeholders": [],
               "manual": [], "counts": {}}

    tpl = os.path.normpath(TEMPLATE)
    if not os.path.isfile(tpl):
        raise RuntimeError("找不到模板骨架：%s" % tpl)
    root, body, names, data, infos = X.load_document(tpl)

    kept = _clear_body_paragraphs(body)

    # 红头第二行：fill 优先，其次 md 标题推断
    name = (fill.get("meeting_name") or "").strip()
    paras = body.findall(X.qn("w:p"))
    if len(paras) >= 2 and name:
        second = name if name.endswith(S.REDHEAD_SUFFIX) else name + S.REDHEAD_SUFFIX
        X.set_paragraph_text(paras[1], second, dict(S.REDHEAD))
        A.format_redhead(paras[1])
        summary["added"].append("红头会议名「%s」" % second)
        # 模板骨架自带浮动红线；万一没保住（比如换了骨架），补下边框
        if X.find_redline(body, S.REDLINE_COLOR) is None:
            A.add_redline(paras[1], summary)
    elif len(paras) >= 2:
        summary["manual"].append(
            "红头第二行沿用了模板的会议名，记得改成本次会议的名称"
            "（或用 --fill 传 meeting_name）")

    at = len(body.findall(X.qn("w:p"))) - 1   # 插入锚点：最后一段之后

    def append(role, text, spec=None):
        nonlocal at
        p = X.new_paragraph()
        if text:
            X.set_paragraph_text(p, text, spec or dict(S.BODY))
        A._insert_after(body, at, p)
        at += 1
        return p

    # 标题
    title = parsed["title"] or (fill.get("title") or "").strip()
    if not title:
        title = A.PLACEHOLDER % "纪要标题"
        summary["placeholders"].append("纪要标题")
    p = append("title", title, dict(S.TITLE))
    A.format_title(p)

    # 信息栏七项，按模板顺序
    for field in S.INFO_FIELDS:
        val = (parsed["info"].get(field["key"])
               or fill.get(field["key"]) or "").strip()
        if not val and field["key"] not in ("attendees", "content"):
            val = A.placeholder_for(field["key"])
            summary["placeholders"].append(field["label"])
        p = append("info", None)
        A.format_info(p, field, val)
        # 出席人员的部门行紧跟其后
        if field["key"] == "attendees":
            for role, text in parsed["blocks"]:
                if role == "dept":
                    d = append("dept", text, dict(S.INFO_VALUE))
                    A.format_dept(d)
            if not val and not any(r == "dept" for r, _ in parsed["blocks"]):
                summary["placeholders"].append("出席人员")

    # 正文
    has_lead = any(r == "lead" for r, _ in parsed["blocks"])
    if not has_lead:
        lead = (fill.get("lead") or "").strip()
        if not lead:
            lead = A.PLACEHOLDER % "帽子段（会议背景概述）"
            summary["placeholders"].append("帽子段")
        p = append("lead", lead)
        A.format_text_role(p, "lead")

    counts = {}
    current_h1_text = ""  # 追踪当前 h1 标题文本，用于区分加粗规则
    for role, text in parsed["blocks"]:
        if role == "dept":
            continue   # 已在信息栏后插入
        if role == "h1":
            current_h1_text = text
        spec = dict(S.spec_for(role) or S.BODY)
        # 加粗规则区分：会议要点下的 1.2.3.（h3）独占一行时加粗；
        # 下一步工作计划下的 1.2.3. 不加粗。
        h3_bold = None
        if role == "h3":
            if "会议要点" in current_h1_text:
                h3_bold = True
            elif "下一步工作" in current_h1_text:
                h3_bold = False
        p = append(role, text, spec)
        A.format_text_role(p, role, text, h3_bold=h3_bold)
        counts[role] = counts.get(role, 0) + 1
    summary["counts"] = counts

    # 页面设置
    mar = X.cm_to_twips(S.PAGE_MARGIN_CM)
    for sect in X.iter_sectPr(root):
        X.set_page_size(sect, S.A4_W, S.A4_H)
        X.set_page_margins(sect, mar, mar, mar, mar,
                           S.PAGE_HEADER_TWIPS, S.PAGE_FOOTER_TWIPS)

    summary["manual"].append("文末缺会议照片 —— 脚本无法生成图片，需手动插入")

    if not output_path:
        base, _ = os.path.splitext(text_path)
        output_path = base + A.OUTPUT_SUFFIX + ".docx"
    data[X.DOC_PART] = X.serialize(root)
    X.write_parts(output_path, names, data, infos)
    warnings = X.validate_docx(output_path)
    if warnings:
        raise RuntimeError("产出文件校验失败：%s" % warnings)
    summary["output"] = output_path
    summary["kept_redhead_paragraphs"] = kept
    return summary


def main():
    ap = argparse.ArgumentParser(description="从 md/txt 生成国开会议纪要 docx")
    ap.add_argument("input", help="md 或 txt 文件")
    ap.add_argument("-o", "--output", help="输出 .docx 路径")
    ap.add_argument("--fill", help="补充内容的 JSON")
    ap.add_argument("--json", action="store_true", help="输出 JSON 摘要")
    args = ap.parse_args()

    if not os.path.isfile(args.input):
        print("ERROR: 文件不存在：%s" % args.input, file=sys.stderr)
        sys.exit(2)

    fill = {}
    if args.fill:
        with io.open(args.fill, encoding="utf-8") as fh:
            fill = json.load(fh)

    try:
        s = build(args.input, args.output, fill)
    except Exception as e:
        print("ERROR: %s" % e, file=sys.stderr)
        sys.exit(1)

    out = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", newline="")
    if args.json:
        out.write(json.dumps(s, ensure_ascii=False, indent=2) + "\n")
    else:
        A.print_summary(s, out)
    out.flush()


if __name__ == "__main__":
    main()
