#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""校验一份会议纪要是否符合国家开放大学会议纪要格式。

用法：
    python check_format.py 纪要.docx            # 人读的报告
    python check_format.py 纪要.docx --json     # 机器读的 JSON

退出码：有 error -> 1，只有 warn/info -> 0。

只读，绝不改输入文件。
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

ERROR, WARN, INFO = "error", "warn", "info"

# 容差：字号允许 ±0（半磅整数比对），行距允许 ±10 twips（0.5 磅），
# 缩进 firstLineChars 允许 ±10（0.1 字符），页边距允许 ±0.05cm
TOL_LINE_TWIPS = 10
TOL_INDENT_CHARS = 10
TOL_MARGIN_CM = 0.05


class Report(object):
    def __init__(self):
        self.items = []

    def add(self, rule, level, message, para=None, actual=None,
            expected=None, fixable=True, fill_key=None):
        self.items.append({
            "rule": rule, "level": level, "message": message,
            "paragraph": para, "actual": actual, "expected": expected,
            "fixable": fixable, "fill_key": fill_key,
        })

    def counts(self):
        c = {ERROR: 0, WARN: 0, INFO: 0}
        for it in self.items:
            c[it["level"]] = c.get(it["level"], 0) + 1
        return c

    @property
    def has_error(self):
        return any(i["level"] == ERROR for i in self.items)


def _fmt_font(run):
    """把 run 的字体信息压成一句可读的话。"""
    bits = []
    if run.get("eastAsia"):
        bits.append(run["eastAsia"])
    if run.get("sz"):
        bits.append("%gpt" % (run["sz"] / 2.0))
    if run.get("bold"):
        bits.append("加粗")
    if run.get("color") and run["color"] != "000000":
        bits.append("#" + run["color"])
    return " ".join(bits) or "未设置"


def _dominant_run(runs):
    """取字数最多的 run 作为该段落的代表格式。"""
    if not runs:
        return None
    return max(runs, key=lambda r: len(r["text"]))


# ---------------- A 页面 ----------------
def check_page(root, rep):
    sects = X.iter_sectPr(root)
    if not sects:
        rep.add("A0", ERROR, "文档没有节属性（sectPr），页面设置无法校验",
                fixable=False)
        return
    for si, sect in enumerate(sects):
        info = X.read_section(sect)
        tag = "" if len(sects) == 1 else "（第%d节）" % (si + 1)
        w, h = info["pgW"], info["pgH"]
        if w and h:
            # A4 允许 11900/11906 这类不同工具的取整差异
            if abs(w - S.A4_W) > 20 or abs(h - S.A4_H) > 20:
                rep.add("A1", ERROR, "纸张不是 A4" + tag,
                        actual="%dx%d twips" % (w, h),
                        expected="%dx%d twips (A4)" % (S.A4_W, S.A4_H))
        for key, cn in (("top", "上"), ("bottom", "下"),
                        ("left", "左"), ("right", "右")):
            tw = info[key]
            if tw is None:
                continue
            cm = X.twips_to_cm(tw)
            if abs(cm - S.PAGE_MARGIN_CM) > TOL_MARGIN_CM:
                rep.add("A2", ERROR, "%s页边距不符%s" % (cn, tag),
                        actual="%.2fcm" % cm,
                        expected="%.2fcm" % S.PAGE_MARGIN_CM)


# ---------------- B 红头 + 红线 ----------------
def check_redhead(body, paras, texts, roles, rep):
    idx = [i for i, r in enumerate(roles) if r == "redhead"]
    if not idx:
        rep.add("B1", ERROR,
                "缺少红头。应为两行居中红字：「%s」+「XX会议纪要」" % S.REDHEAD_ORG,
                expected="国家开放大学 / XX（项目/工作）会议纪要",
                fill_key="meeting_name")
    else:
        first = texts[idx[0]].strip()
        if first != S.REDHEAD_ORG:
            rep.add("B2", WARN, "红头第一行应为「%s」" % S.REDHEAD_ORG,
                    para=idx[0], actual=first, expected=S.REDHEAD_ORG)
        if len(idx) < 2:
            rep.add("B3", ERROR, "红头缺第二行（会议名称，须以「会议纪要」结尾）",
                    para=idx[0], fill_key="meeting_name")
        else:
            second = texts[idx[1]].strip()
            if not second.endswith(S.REDHEAD_SUFFIX):
                rep.add("B4", WARN,
                        "红头第二行应以「%s」结尾" % S.REDHEAD_SUFFIX,
                        para=idx[1], actual=second)

        spec = S.REDHEAD
        for i in idx:
            run = _dominant_run(X.read_paragraph_runs(paras[i]))
            if run is None:
                continue
            if run["eastAsia"] != spec["font"]:
                rep.add("B5", ERROR, "红头字体不符", para=i,
                        actual=run["eastAsia"] or "未设置", expected=spec["font"])
            if run["sz"] != X.pt_to_half_points(spec["size_pt"]):
                rep.add("B6", ERROR, "红头字号不符", para=i,
                        actual=_fmt_font(run),
                        expected="%gpt" % spec["size_pt"])
            if not run["bold"]:
                rep.add("B7", ERROR, "红头应加粗", para=i, actual="未加粗")
            if (run["color"] or "") != spec["color"]:
                rep.add("B8", ERROR, "红头应为红色", para=i,
                        actual="#" + (run["color"] or "自动"),
                        expected="#" + spec["color"])
            props = X.read_paragraph_props(paras[i])
            if props["align"] != spec["align"]:
                rep.add("B9", ERROR, "红头应居中", para=i,
                        actual=props["align"] or "左对齐", expected="居中")
            _check_line(props, spec["line_pt"], "B10", "红头", i, rep)

    # 红线
    rl = X.find_redline(body, S.REDLINE_COLOR)
    if rl is None:
        rep.add("B11", ERROR,
                "缺少红头下方的红色横线（公文惯例，模板为 3 磅红线）",
                expected="红色横线 #%s，线宽 3 磅" % S.REDLINE_COLOR)
    else:
        li, lw = rl
        if lw is not None and abs(lw - S.REDLINE_WIDTH_EMU) > 6350:  # ±0.5pt
            rep.add("B12", WARN, "红线线宽与模板不一致", para=li,
                    actual="%.2g磅" % (lw / 12700.0),
                    expected="%.2g磅" % (S.REDLINE_WIDTH_EMU / 12700.0),
                    fixable=False)
    # B13 位置：红线必须在红头末行下方，且别处不能有多余的红线。
    # 画到标题下面是最常见的错法，光查"有没有线"查不出来 —— find_redline()
    # 只返回第一条，多出来的那条得单独扫。
    if idx:
        want = max(idx)
        for i, p in enumerate(paras):
            if i == want:
                continue
            if X.paragraph_has_redline_border(p, S.REDLINE_COLOR):
                rep.add("B13", ERROR, "红线画在了红头末行以外的段落下方",
                        para=i,
                        actual="第%d段「%s」下方有红线"
                               % (i + 1, texts[i].strip()[:16] or "空段"),
                        expected="红线只应在红头末行（第%d段「%s」）下方"
                                 % (want + 1, texts[want].strip()[:16]))


# ---------------- C 标题 ----------------
def check_title(paras, texts, roles, rep):
    idx = [i for i, r in enumerate(roles) if r == "title"]
    if not idx:
        rep.add("C1", ERROR, "缺少纪要标题（红头与信息栏之间的居中标题行）",
                fill_key="title")
        return
    if len(idx) > 1:
        rep.add("C2", WARN,
                "标题占了 %d 个段落，正式纪要通常为一行；确认是否需要合并"
                % len(idx),
                para=idx[0],
                actual=" / ".join(texts[i].strip()[:20] for i in idx),
                fixable=False)
    spec = S.TITLE
    for i in idx:
        run = _dominant_run(X.read_paragraph_runs(paras[i]))
        if run is None:
            continue
        if run["eastAsia"] != spec["font"]:
            rep.add("C3", ERROR, "标题字体不符", para=i,
                    actual=run["eastAsia"] or "未设置", expected=spec["font"])
        if run["sz"] != X.pt_to_half_points(spec["size_pt"]):
            rep.add("C4", ERROR, "标题字号不符", para=i,
                    actual=_fmt_font(run), expected="%gpt（小二）" % spec["size_pt"])
        props = X.read_paragraph_props(paras[i])
        if props["align"] != spec["align"]:
            rep.add("C5", ERROR, "标题应居中", para=i,
                    actual=props["align"] or "左对齐", expected="居中")
        _check_line(props, spec["line_pt"], "C6", "标题", i, rep)


# ---------------- D 信息栏 ----------------
def check_info(paras, texts, roles, rep):
    found = {}
    order = []
    for i, r in enumerate(roles):
        if r != "info":
            continue
        f = S.match_info_label(texts[i])
        if f:
            found.setdefault(f["key"], i)
            order.append((f["key"], i))

    # D1 缺项
    for f in S.INFO_FIELDS:
        if f["key"] not in found:
            rep.add("D1", ERROR if f["required"] else WARN,
                    "信息栏缺「%s」" % f["label"],
                    expected="%s：%s" % (f["label"], f["hint"]),
                    fill_key=f["key"])

    # D2 顺序
    want = [f["key"] for f in S.INFO_FIELDS if f["key"] in found]
    got = [k for k, _ in order]
    if got != want:
        rep.add("D2", WARN, "信息栏顺序与模板不一致",
                para=order[0][1] if order else None,
                actual=" → ".join(S.INFO_BY_KEY[k]["label"] for k in got),
                expected=" → ".join(S.INFO_BY_KEY[k]["label"] for k in want))

    # D3/D4/D5 每项的标签补齐与双字体
    for key, i in order:
        field = S.INFO_BY_KEY[key]
        raw = texts[i]
        sep = "：" if "：" in raw else (":" if ":" in raw else None)
        if sep is None:
            continue
        actual_label = raw.split(sep, 1)[0]
        expect_label = X.pad_label(field["label"], S.INFO_LABEL_WIDTH_HAN)
        if actual_label != expect_label:
            rep.add("D3", WARN,
                    "「%s」标签未按模板补齐到 %d 个汉字宽"
                    % (field["label"], S.INFO_LABEL_WIDTH_HAN),
                    para=i, actual=repr(actual_label),
                    expected=repr(expect_label))
        if sep == ":":
            rep.add("D4", WARN, "「%s」用了半角冒号" % field["label"],
                    para=i, actual="半角 :", expected="全角 ：")

        runs = X.read_paragraph_runs(paras[i])
        if not runs:
            continue
        label_run = runs[0]
        if label_run["eastAsia"] != S.INFO_LABEL["font"]:
            rep.add("D5", ERROR, "「%s」标签字体应为%s"
                    % (field["label"], S.INFO_LABEL["font"]),
                    para=i, actual=label_run["eastAsia"] or "未设置",
                    expected=S.INFO_LABEL["font"])
        if label_run["sz"] != X.pt_to_half_points(S.INFO_LABEL["size_pt"]):
            rep.add("D6", ERROR, "「%s」标签字号应为三号" % field["label"],
                    para=i, actual=_fmt_font(label_run),
                    expected="%gpt（三号）" % S.INFO_LABEL["size_pt"])
        # 值部分：标签之后的 run
        value_runs = [r for r in runs[1:] if r["text"].strip(" ：:")]
        vrun = _dominant_run(value_runs)
        if vrun is not None:
            if vrun["eastAsia"] != S.INFO_VALUE["font"]:
                rep.add("D7", ERROR, "「%s」内容字体应为%s"
                        % (field["label"], S.INFO_VALUE["font"]),
                        para=i, actual=vrun["eastAsia"] or "未设置",
                        expected=S.INFO_VALUE["font"])
            if vrun["sz"] != X.pt_to_half_points(S.INFO_VALUE["size_pt"]):
                rep.add("D8", ERROR, "「%s」内容字号应为三号" % field["label"],
                        para=i, actual=_fmt_font(vrun),
                        expected="%gpt（三号）" % S.INFO_VALUE["size_pt"])
        elif key not in ("attendees", "content"):
            # 出席人员/会议内容的值在下方分行，本段可以只有标签
            rep.add("D9", ERROR, "「%s」没有填写内容" % field["label"],
                    para=i, fill_key=key)

    # D10 出席人员下方部门行
    dept_idx = [i for i, r in enumerate(roles) if r == "dept"]
    if "attendees" in found and not dept_idx:
        after = texts[found["attendees"]].split("：", 1)
        if len(after) < 2 or not after[1].strip():
            rep.add("D10", ERROR,
                    "「出席人员」既没有同行内容，下方也没有分部门的人员行",
                    para=found["attendees"], fill_key="attendees")
    for i in dept_idx:
        run = _dominant_run(X.read_paragraph_runs(paras[i]))
        if run and run["eastAsia"] != S.INFO_VALUE["font"]:
            rep.add("D11", ERROR, "出席人员行字体应为%s" % S.INFO_VALUE["font"],
                    para=i, actual=run["eastAsia"] or "未设置",
                    expected=S.INFO_VALUE["font"])


# ---------------- E 帽子段 ----------------
def check_lead(texts, roles, rep):
    if "lead" in roles:
        i = roles.index("lead")
        t = texts[i].strip()
        if len(t) < 20:
            rep.add("E2", WARN,
                    "帽子段偏短（%d 字），通常要交代会议背景与总体安排" % len(t),
                    para=i, actual=t[:30], fixable=False)
        return
    if "content" not in [S.match_info_label(texts[i])["key"]
                         for i, r in enumerate(roles)
                         if r == "info" and S.match_info_label(texts[i])]:
        return   # 连「会议内容：」都没有，D1 已经报过
    rep.add("E1", ERROR,
            "「会议内容：」之后缺帽子段（概述会议背景的无编号段落），"
            "不能直接进「一、」",
            expected="如：根据《…》的有关工作部署，X部与Y部就…进行沟通，"
                     "明确工作安排如下：",
            fill_key="lead")


# ---------------- F 标题层级 ----------------
def check_headings(paras, texts, roles, rep):
    seen = []
    for i, r in enumerate(roles):
        lvl = S.heading_level(r)
        if lvl is None:
            continue
        seen.append((i, lvl))
        spec = S.spec_for(r)
        runs = X.read_paragraph_runs(paras[i])

        # 「（一）登录认证演示。演示了……」标题与说明合排：标题部分该用标题
        # 字体并加粗，说明部分保持正文字体。这时不能拿最长的 run 当代表 ——
        # 最长的是说明文字，会把正确的排版误报成字体不符。
        parts = S.split_inline_heading(texts[i])
        if parts and runs:
            head_run = runs[0]
            if head_run["eastAsia"] != spec["font"]:
                rep.add("F1", ERROR,
                        "%d级标题（与说明合排）的标题部分字体应为%s"
                        % (lvl, spec["font"]),
                        para=i, actual=head_run["eastAsia"] or "未设置",
                        expected=spec["font"])
            if not head_run["bold"]:
                rep.add("F4", WARN,
                        "%d级标题与说明写在同一段时，标题部分建议加粗以作区分" % lvl,
                        para=i, actual="未加粗",
                        expected="标题部分加粗，说明部分用%s" % S.BODY["font"])
            tail = _dominant_run([x for x in runs[1:] if x["text"].strip()])
            if tail and tail["eastAsia"] != S.BODY["font"]:
                rep.add("F5", ERROR,
                        "%d级标题后的说明文字字体应为%s" % (lvl, S.BODY["font"]),
                        para=i, actual=tail["eastAsia"] or "未设置",
                        expected=S.BODY["font"])
            for run in runs:
                if run["sz"] != X.pt_to_half_points(spec["size_pt"]):
                    rep.add("F2", ERROR, "%d级标题字号应为三号" % lvl, para=i,
                            actual=_fmt_font(run),
                            expected="%gpt（三号）" % spec["size_pt"])
                    break
            continue

        run = _dominant_run(runs)
        if run:
            if run["eastAsia"] != spec["font"]:
                rep.add("F1", ERROR, "%d级标题字体应为%s" % (lvl, spec["font"]),
                        para=i, actual=run["eastAsia"] or "未设置",
                        expected=spec["font"])
            if run["sz"] != X.pt_to_half_points(spec["size_pt"]):
                rep.add("F2", ERROR, "%d级标题字号应为三号" % lvl, para=i,
                        actual=_fmt_font(run),
                        expected="%gpt（三号）" % spec["size_pt"])
    # 跳级。prev 从 0 起 —— 首个标题不是「一、」也算跳级，
    # 不然「正文直接从 1. 开始」这种会漏掉。
    prev = 0
    for i, lvl in seen:
        if lvl > prev + 1:
            rep.add("F3", WARN,
                    "标题层级跳级：%s直接到 %d级"
                    % ("从正文" if prev == 0 else "%d级 " % prev, lvl),
                    para=i, actual=texts[i].strip()[:24],
                    expected="补上中间层级，或改为 %d级" % (prev + 1),
                    fixable=False)
        prev = lvl


# ---------------- G 正文 ----------------
def check_body(paras, texts, roles, rep):
    for i, r in enumerate(roles):
        if r not in ("body", "lead"):
            continue
        spec = S.BODY
        run = _dominant_run(X.read_paragraph_runs(paras[i]))
        if run:
            if run["eastAsia"] != spec["font"]:
                rep.add("G1", ERROR, "正文字体应为%s" % spec["font"], para=i,
                        actual=run["eastAsia"] or "未设置", expected=spec["font"])
            if run["sz"] != X.pt_to_half_points(spec["size_pt"]):
                rep.add("G2", ERROR, "正文字号应为三号", para=i,
                        actual=_fmt_font(run),
                        expected="%gpt（三号）" % spec["size_pt"])
        props = X.read_paragraph_props(paras[i])
        flc = props["firstLineChars"]
        if flc is None or abs(flc - spec["indent_chars"]) > TOL_INDENT_CHARS:
            rep.add("G3", ERROR, "正文应首行缩进 2 字符", para=i,
                    actual="%s" % ("无缩进" if flc is None else "%.2g字符" % (flc / 100.0)),
                    expected="2 字符")
        if props["align"] != spec["align"]:
            rep.add("G4", WARN, "正文应两端对齐", para=i,
                    actual=props["align"] or "未设置", expected="两端对齐")
        _check_line(props, spec["line_pt"], "G5", "正文", i, rep)


def _check_line(props, want_pt, rule, what, para, rep):
    line, rule_ = props["line"], props["lineRule"]
    want = X.pt_to_twips(want_pt)
    if line is None:
        rep.add(rule, ERROR, "%s没有设置固定行距" % what, para=para,
                actual="未设置", expected="固定值 %g 磅" % want_pt)
        return
    if rule_ != "exact" or abs(line - want) > TOL_LINE_TWIPS:
        rep.add(rule, ERROR, "%s行距不符" % what, para=para,
                actual="%s %.3g磅" % ("固定" if rule_ == "exact" else (rule_ or "多倍"),
                                     line / 20.0),
                expected="固定值 %g 磅" % want_pt)


# ---------------- H 结尾照片 ----------------
def check_photo(paras, texts, roles, rep):
    if "photo" not in roles:
        # 模板文字要求"最后附一张照片"，但模板本身没有图片部件
        rep.add("H1", WARN,
                "文末没有会议照片。模板要求纪要末尾附一张会议照片，"
                "请自行插入（脚本无法生成图片）",
                expected="文末插入一张会议现场照片", fixable=False)
        return

    # H2 照片被固定行距裁掉。lineRule="exact" 是硬裁剪，Word 不会为图片
    # 长高 —— 11.92cm 的照片碰上 28 磅行距只剩 0.99cm，露出顶上一条。
    for i, r in enumerate(roles):
        if r != "photo":
            continue
        props = X.read_paragraph_props(paras[i])
        if props["lineRule"] == "exact":
            shown = (props["line"] or 0) / 20.0
            rep.add("H2", WARN,
                    "照片段用了固定行距，图片会被裁掉（只显示顶部一条）",
                    para=i, actual="固定 %.3g 磅" % shown,
                    expected="单倍/自适应行距（lineRule=auto），由图片高度撑开")


# ---------------- I 西文字体 ----------------
def check_western(paras, roles, rep):
    bad = []
    for i, r in enumerate(roles):
        if r == "blank":
            continue
        for run in X.read_paragraph_runs(paras[i]):
            if run["ascii"] and run["ascii"] != S.WESTERN:
                bad.append((i, run["ascii"]))
                break
    if bad:
        rep.add("I1", WARN,
                "有 %d 处西文/数字字体不是 %s" % (len(bad), S.WESTERN),
                para=bad[0][0], actual=bad[0][1], expected=S.WESTERN)


# ---------------- 主流程 ----------------
def check_file(path):
    root, body, names, data, infos = X.load_document(path)
    paras = X.top_level_paragraphs(body)
    texts = [X.paragraph_text(p) for p in paras]
    roles = S.classify_all(texts, paras)

    rep = Report()
    check_page(root, rep)
    check_redhead(body, paras, texts, roles, rep)
    check_title(paras, texts, roles, rep)
    check_info(paras, texts, roles, rep)
    check_lead(texts, roles, rep)
    check_headings(paras, texts, roles, rep)
    check_body(paras, texts, roles, rep)
    check_photo(paras, texts, roles, rep)
    check_western(paras, roles, rep)

    outline = [{"index": i, "role": r, "text": texts[i].strip()[:40]}
               for i, r in enumerate(roles) if r != "blank"]
    return rep, outline, roles


GROUP_NAMES = {
    "A": "页面设置", "B": "红头与红线", "C": "标题", "D": "信息栏",
    "E": "帽子段", "F": "标题层级", "G": "正文", "H": "结尾照片",
    "I": "西文字体",
}
LEVEL_CN = {ERROR: "必改", WARN: "建议", INFO: "提示"}


def print_report(path, rep, outline, out):
    out.write("会议纪要格式校验：%s\n" % os.path.basename(path))
    out.write("=" * 60 + "\n\n")
    c = rep.counts()
    if not rep.items:
        out.write("完全符合国家开放大学会议纪要格式，没有发现问题。\n")
        return
    out.write("共 %d 项：必改 %d，建议 %d，提示 %d\n\n"
              % (len(rep.items), c[ERROR], c[WARN], c[INFO]))

    by_group = {}
    for it in rep.items:
        by_group.setdefault(it["rule"][0], []).append(it)
    for g in sorted(by_group):
        out.write("【%s】%s\n" % (g, GROUP_NAMES.get(g, "")))
        for it in by_group[g]:
            loc = "第%d段" % (it["paragraph"] + 1) if it["paragraph"] is not None else "全文"
            out.write("  [%s] %s %s：%s\n"
                      % (it["rule"], LEVEL_CN[it["level"]], loc, it["message"]))
            if it["actual"] is not None:
                out.write("        实际：%s\n" % it["actual"])
            if it["expected"] is not None:
                out.write("        应为：%s\n" % it["expected"])
            if not it["fixable"]:
                out.write("        （需人工处理，一键改写修不了）\n")
        out.write("\n")

    need_fill = sorted({it["fill_key"] for it in rep.items if it["fill_key"]})
    if need_fill:
        labels = [S.fill_label(k) for k in need_fill]
        out.write("需要补充内容的项：%s\n" % "、".join(labels))
        out.write("（一键改写不会自行编写这些内容，需先提供）\n\n")

    if any(f in (it["actual"] or "") for it in rep.items
           for f in S.FONTS_MAY_MISSING):
        pass
    out.write("提示：%s 未安装在本机时，Word/WPS 会替换显示，"
              "但文件里存的字体名是正确的。\n" % "、".join(S.FONTS_MAY_MISSING))


def main():
    ap = argparse.ArgumentParser(
        description="校验会议纪要是否符合国家开放大学会议纪要格式")
    ap.add_argument("input", help="待校验的 .docx")
    ap.add_argument("--json", action="store_true", help="输出 JSON")
    ap.add_argument("--outline", action="store_true", help="附带段落角色清单")
    args = ap.parse_args()

    if not os.path.isfile(args.input):
        print("ERROR: 文件不存在：%s" % args.input, file=sys.stderr)
        sys.exit(2)
    if not args.input.lower().endswith(".docx"):
        print("ERROR: 只支持 .docx（.doc 请先转换）", file=sys.stderr)
        sys.exit(2)

    try:
        rep, outline, roles = check_file(args.input)
    except Exception as e:
        print("ERROR: %s" % e, file=sys.stderr)
        sys.exit(2)

    out = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", newline="")
    if args.json:
        payload = {
            "file": args.input,
            "counts": rep.counts(),
            "has_error": rep.has_error,
            "findings": rep.items,
            "need_fill": sorted({i["fill_key"] for i in rep.items if i["fill_key"]}),
        }
        # 给编排层用：fill_key -> 中文标签，问用户时直接照着念
        payload["need_fill_labels"] = {
            k: S.fill_label(k) for k in payload["need_fill"]}
        if args.outline:
            payload["outline"] = outline
        out.write(json.dumps(payload, ensure_ascii=False, indent=2))
        out.write("\n")
    else:
        print_report(args.input, rep, outline, out)
        if args.outline:
            out.write("\n段落角色：\n")
            for o in outline:
                out.write("  %2d %-8s %s\n"
                          % (o["index"] + 1, o["role"], o["text"]))
    out.flush()
    sys.exit(1 if rep.has_error else 0)


if __name__ == "__main__":
    main()
