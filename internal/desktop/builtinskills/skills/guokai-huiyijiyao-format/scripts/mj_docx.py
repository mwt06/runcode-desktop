#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""OpenXML (WordprocessingML) 底层读写 —— 会议纪要格式校验/改写共用。

纯标准库。单位遵循 OOXML 约定：
- twip (DXA)：1 英寸 = 1440 twips，1 cm = 1440/2.54 twips
- 字号 sz 用半磅（三号 16pt -> 32）
- 固定行距 line 用 twips（28 磅 -> 560）

与两个兄弟公文 skill 的 openxml_utils.py 同路线，但多两项能力：
读 run 属性（校验要读，兄弟 skill 只会写）、同段落双字体（信息栏标签/值）。
"""
import re
import zipfile
from xml.etree import ElementTree as ET

W_NS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
R_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"

DOC_PART = "word/document.xml"

# ET 会把没登记过的命名空间前缀重写成 ns0/ns1/…，而根标签上
# mc:Ignorable="w14 w15 wp14" 里点到的还是原名。那些前缀就此失去声明，
# **Word 直接拒绝打开报「文件可能已经损坏」，但 XML 依然合法可解析** ——
# testzip() 和 ET.fromstring() 都查不出来。
# 两道防线：① 这张表把标准前缀全登记上；② serialize() 把根标签换回原始字节，
# 保住 WPS 自定义前缀那类表里没有的。
NS_PREFIXES = [
    ("w", W_NS),
    ("r", R_NS),
    ("mc", "http://schemas.openxmlformats.org/markup-compatibility/2006"),
    ("m", "http://schemas.openxmlformats.org/officeDocument/2006/math"),
    ("wp", "http://schemas.openxmlformats.org/drawingml/2006/"
           "wordprocessingDrawing"),
    ("a", "http://schemas.openxmlformats.org/drawingml/2006/main"),
    ("pic", "http://schemas.openxmlformats.org/drawingml/2006/picture"),
    ("o", "urn:schemas-microsoft-com:office:office"),
    ("v", "urn:schemas-microsoft-com:vml"),
    ("w10", "urn:schemas-microsoft-com:office:word"),
    ("w14", "http://schemas.microsoft.com/office/word/2010/wordml"),
    ("w15", "http://schemas.microsoft.com/office/word/2012/wordml"),
    ("w16se", "http://schemas.microsoft.com/office/word/2015/wordml/symex"),
    ("w16cid", "http://schemas.microsoft.com/office/word/2016/wordml/cid"),
    ("wne", "http://schemas.microsoft.com/office/word/2006/wordml"),
    ("wpc", "http://schemas.microsoft.com/office/word/2010/"
            "wordprocessingCanvas"),
    ("wpg", "http://schemas.microsoft.com/office/word/2010/"
            "wordprocessingGroup"),
    ("wpi", "http://schemas.microsoft.com/office/word/2010/wordprocessingInk"),
    ("wps", "http://schemas.microsoft.com/office/word/2010/"
            "wordprocessingShape"),
    ("wp14", "http://schemas.microsoft.com/office/word/2010/"
             "wordprocessingDrawing"),
    ("a14", "http://schemas.microsoft.com/office/drawing/2010/main"),
    ("wpsCustomData", "http://www.wps.cn/officeDocument/2013/wpsCustomData"),
]

# 原始根起始标签，按 id(root) 存。serialize() 拿它换回 ET 生成的那个。
_ORIG_ROOT_TAG = {}


def register():
    for prefix, uri in NS_PREFIXES:
        ET.register_namespace(prefix, uri)


def qn(tag):
    """'w:p' -> '{ns}p'。已限定的原样返回。"""
    if tag.startswith("w:"):
        return "{%s}%s" % (W_NS, tag[2:])
    if tag.startswith("r:"):
        return "{%s}%s" % (R_NS, tag[2:])
    return tag


# ---------------- 单位换算 ----------------
def cm_to_twips(cm):
    return int(round(cm / 2.54 * 1440))


def twips_to_cm(tw):
    return float(tw) * 2.54 / 1440


def pt_to_half_points(pt):
    return int(round(pt * 2))


def pt_to_twips(pt):
    return int(round(pt * 20))


# ---------------- 元素helper ----------------
def get_or_make(parent, short_tag):
    """按短标签找直接子元素，没有就建。

    pPr/rPr 在 OOXML 里必须排在最前，新建时插到 index 0。
    """
    el = parent.find(qn(short_tag))
    if el is None:
        el = ET.SubElement(parent, qn(short_tag))
        if short_tag in ("w:pPr", "w:rPr"):
            parent.remove(el)
            parent.insert(0, el)
    return el


def set_attr(el, short_attr, value):
    el.set(qn(short_attr), str(value))


def get_attr(el, short_attr, default=None):
    return el.get(qn(short_attr), default)


def del_attr(el, short_attr):
    if get_attr(el, short_attr) is not None:
        del el.attrib[qn(short_attr)]


def remove_children(parent, short_tag):
    for child in list(parent.findall(qn(short_tag))):
        parent.remove(child)


def paragraph_text(p):
    """拼接段落内所有 w:t 的可见文字。"""
    return "".join(t.text or "" for t in p.iter(qn("w:t")))


def has_drawing(p):
    """段落里是否有图片/图形（w:drawing 或 w:pict）。"""
    return p.find(".//" + qn("w:drawing")) is not None or \
        p.find(".//" + qn("w:pict")) is not None


A_NS = "http://schemas.openxmlformats.org/drawingml/2006/main"


def has_image(p):
    """段落里是否有真正的**图片**（a:blip 引用图片部件），
    区别于线条/形状。结尾照片的校验靠这个，别被红线误判。"""
    return p.find(".//{%s}blip" % A_NS) is not None or \
        p.find(".//" + qn("w:pict") + "/{urn:schemas-microsoft-com:vml}shape"
               "/{urn:schemas-microsoft-com:vml}imagedata") is not None


def find_redline(body, color="FF0000"):
    """找红头下方的红色横线。两种实现都认：

    1. 浮动 line 形状（模板的做法）：prstGeom prst="line" + a:ln/solidFill/srgbClr
    2. 段落下边框（本脚本生成的做法）：w:pBdr/w:bottom 红色

    返回 (段落索引, 线宽EMU) 或 None。EMU 便于统一比对：
    边框 w:sz 是 1/8 磅，1 磅 = 12700 EMU。
    """
    paras = body.findall(qn("w:p"))
    for i, p in enumerate(paras):
        # 浮动形状
        for ln in p.iter("{%s}ln" % A_NS):
            clr = ln.find(".//{%s}srgbClr" % A_NS)
            if clr is not None and (clr.get("val") or "").upper() == color:
                w = ln.get("w")
                try:
                    w = int(w) if w is not None else None
                except ValueError:
                    w = None
                return i, w
        # 段落下边框
        ppr = p.find(qn("w:pPr"))
        if ppr is None:
            continue
        bdr = ppr.find(qn("w:pBdr"))
        if bdr is None:
            continue
        bottom = bdr.find(qn("w:bottom"))
        if bottom is None:
            continue
        if (get_attr(bottom, "w:color") or "").upper() != color:
            continue
        if get_attr(bottom, "w:val") in ("none", "nil"):
            continue
        sz = _int_or_none(get_attr(bottom, "w:sz"))
        # w:sz 单位 1/8 磅 -> EMU
        return i, (int(round(sz / 8.0 * 12700)) if sz else None)
    return None


def set_redline_border(p, color="FF0000", sz=24, space=8):
    """在段落下方画红线 —— 用段落下边框实现。

    sz 单位 1/8 磅（24 = 3 磅），space 单位磅。
    比克隆浮动形状可靠：不需要 drawing/rels/Content_Types，
    也不会在用户编辑时被挪走。
    """
    ppr = get_or_make(p, "w:pPr")
    bdr = ppr.find(qn("w:pBdr"))
    if bdr is None:
        bdr = ET.Element(qn("w:pBdr"))
        # w:pBdr 在 pPr 里的位置有讲究：跟在 pStyle/keepNext 之类之后、
        # spacing/jc 之前。插在 spacing 前面即可满足 schema。
        anchor = None
        for tag in ("w:spacing", "w:ind", "w:jc", "w:rPr"):
            el = ppr.find(qn(tag))
            if el is not None:
                anchor = el
                break
        if anchor is not None:
            ppr.insert(list(ppr).index(anchor), bdr)
        else:
            ppr.append(bdr)
    remove_children(bdr, "w:bottom")
    bottom = ET.SubElement(bdr, qn("w:bottom"))
    set_attr(bottom, "w:val", "single")
    set_attr(bottom, "w:sz", sz)
    set_attr(bottom, "w:space", space)
    set_attr(bottom, "w:color", color)


def paragraph_has_redline_border(p, color="FF0000"):
    """这个段落自己带红色下边框吗（下边框实现的红线）。

    区别于 find_redline()：那个扫全篇、两种实现都认；这个只问单段、
    只认下边框 —— 纠正线的位置时不能碰模板自带的浮动形状。
    """
    ppr = p.find(qn("w:pPr"))
    if ppr is None:
        return False
    bdr = ppr.find(qn("w:pBdr"))
    if bdr is None:
        return False
    bottom = bdr.find(qn("w:bottom"))
    if bottom is None:
        return False
    if (get_attr(bottom, "w:color") or "").upper() != color:
        return False
    return get_attr(bottom, "w:val") not in ("none", "nil")


def clear_redline_border(p):
    ppr = p.find(qn("w:pPr"))
    if ppr is None:
        return
    bdr = ppr.find(qn("w:pBdr"))
    if bdr is None:
        return
    remove_children(bdr, "w:bottom")
    if not list(bdr):
        ppr.remove(bdr)


# ---------------- 读：段落属性 ----------------
def read_paragraph_props(p):
    """读段落格式，返回 dict。缺的键为 None，表示"继承/未设置"。"""
    out = {"align": None, "line": None, "lineRule": None,
           "before": None, "after": None,
           "firstLineChars": None, "firstLine": None,
           "leftChars": None, "rightChars": None, "style": None}
    ppr = p.find(qn("w:pPr"))
    if ppr is None:
        return out
    jc = ppr.find(qn("w:jc"))
    if jc is not None:
        out["align"] = get_attr(jc, "w:val")
    st = ppr.find(qn("w:pStyle"))
    if st is not None:
        out["style"] = get_attr(st, "w:val")
    sp = ppr.find(qn("w:spacing"))
    if sp is not None:
        for k, a in (("line", "w:line"), ("lineRule", "w:lineRule"),
                     ("before", "w:before"), ("after", "w:after")):
            v = get_attr(sp, a)
            if v is not None:
                out[k] = v if k == "lineRule" else _int_or_none(v)
    ind = ppr.find(qn("w:ind"))
    if ind is not None:
        for k, a in (("firstLineChars", "w:firstLineChars"),
                     ("firstLine", "w:firstLine"),
                     ("leftChars", "w:leftChars"),
                     ("rightChars", "w:rightChars")):
            out[k] = _int_or_none(get_attr(ind, a))
    return out


def _int_or_none(v):
    if v is None:
        return None
    try:
        return int(v)
    except (TypeError, ValueError):
        return None


def read_run_props(rpr):
    """读 w:rPr，返回 dict（eastAsia/ascii/hAnsi/sz/bold/color）。"""
    out = {"eastAsia": None, "ascii": None, "hAnsi": None,
           "sz": None, "bold": False, "color": None}
    if rpr is None:
        return out
    fonts = rpr.find(qn("w:rFonts"))
    if fonts is not None:
        out["eastAsia"] = get_attr(fonts, "w:eastAsia")
        out["ascii"] = get_attr(fonts, "w:ascii")
        out["hAnsi"] = get_attr(fonts, "w:hAnsi")
    sz = rpr.find(qn("w:sz"))
    if sz is not None:
        out["sz"] = _int_or_none(get_attr(sz, "w:val"))
    b = rpr.find(qn("w:b"))
    if b is not None:
        # <w:b/> 或 <w:b w:val="1"/> 为真；w:val="0"/"false" 为假
        out["bold"] = get_attr(b, "w:val") not in ("0", "false")
    col = rpr.find(qn("w:color"))
    if col is not None:
        out["color"] = (get_attr(col, "w:val") or "").upper() or None
    return out


def read_paragraph_runs(p):
    """读段落内每个 run 的文字与格式。

    返回 [{"text":…, "eastAsia":…, "ascii":…, "hAnsi":…,
            "sz":…, "bold":…, "color":…}, …]，只含有文字的 run。
    校验器靠这个判断字体字号，兄弟 skill 的 core 只会写不会读。
    """
    runs = []
    for r in p.findall(qn("w:r")):
        text = "".join(t.text or "" for t in r.iter(qn("w:t")))
        if not text:
            continue
        info = read_run_props(r.find(qn("w:rPr")))
        info["text"] = text
        runs.append(info)
    return runs


def merge_runs_by_format(runs):
    """把格式相同的相邻 run 合成一段，便于报告与判定。"""
    merged = []
    for r in runs:
        key = (r["eastAsia"], r["ascii"], r["sz"], r["bold"], r["color"])
        if merged and merged[-1][0] == key:
            merged[-1][1]["text"] += r["text"]
        else:
            merged.append((key, dict(r)))
    return [m[1] for m in merged]


# ---------------- 写：段落属性 ----------------
def set_paragraph_spacing(p, line_twips, line_rule="exact", before=0, after=0):
    ppr = get_or_make(p, "w:pPr")
    sp = get_or_make(ppr, "w:spacing")
    set_attr(sp, "w:line", line_twips)
    set_attr(sp, "w:lineRule", line_rule)
    set_attr(sp, "w:before", before)
    set_attr(sp, "w:after", after)
    # 去掉 *Lines 变体，否则 before/after 不生效
    for a in ("w:beforeLines", "w:afterLines",
              "w:beforeAutospacing", "w:afterAutospacing"):
        del_attr(sp, a)


def set_paragraph_spacing_auto(p, before=0, after=0):
    """行距交给 Word 自适应（lineRule="auto"），照片段用。

    固定行距（exact）是硬裁剪，会把 inline 图片按行高切掉 ——
    11.92cm 的照片碰上 28 磅行距只剩 0.99cm。auto 让 Word 按图片
    实际高度撑开行。实测 33 份真实纪要的照片段全部是 auto。
    """
    ppr = get_or_make(p, "w:pPr")
    sp = get_or_make(ppr, "w:spacing")
    set_attr(sp, "w:line", 240)          # 单倍行距 = 240 (1/240 行)
    set_attr(sp, "w:lineRule", "auto")
    set_attr(sp, "w:before", before)
    set_attr(sp, "w:after", after)
    for a in ("w:beforeLines", "w:afterLines",
              "w:beforeAutospacing", "w:afterAutospacing"):
        del_attr(sp, a)


def set_alignment(p, val):
    """val ∈ {'left','center','right','both'}。"""
    ppr = get_or_make(p, "w:pPr")
    jc = get_or_make(ppr, "w:jc")
    set_attr(jc, "w:val", val)


def set_first_line_indent_chars(p, chars=200, size_half_points=32):
    """按字符数设首行缩进。firstLineChars 是权威值，firstLine 给旧版兜底。"""
    ppr = get_or_make(p, "w:pPr")
    ind = get_or_make(ppr, "w:ind")
    for a in ("w:firstLine", "w:firstLineChars", "w:hanging", "w:hangingChars"):
        del_attr(ind, a)
    set_attr(ind, "w:firstLineChars", chars)
    # 字号整磅对应 twips = 半磅 * 10
    set_attr(ind, "w:firstLine", int(round(chars / 100.0 * size_half_points * 10)))


def clear_first_line_indent(p):
    ppr = p.find(qn("w:pPr"))
    if ppr is None:
        return
    ind = ppr.find(qn("w:ind"))
    if ind is None:
        return
    for a in ("w:firstLine", "w:firstLineChars", "w:hanging", "w:hangingChars"):
        del_attr(ind, a)


# ---------------- 写：run 属性 ----------------
def apply_run_props(rpr, font, size_half_points, bold=False, color=None):
    fonts = get_or_make(rpr, "w:rFonts")
    set_attr(fonts, "w:ascii", "Times New Roman")
    set_attr(fonts, "w:hAnsi", "Times New Roman")
    set_attr(fonts, "w:eastAsia", font)
    set_attr(fonts, "w:cs", "Times New Roman")
    set_attr(fonts, "w:hint", "eastAsia")
    for tag in ("w:sz", "w:szCs"):
        set_attr(get_or_make(rpr, tag), "w:val", size_half_points)
    remove_children(rpr, "w:b")
    remove_children(rpr, "w:bCs")
    if bold:
        ET.SubElement(rpr, qn("w:b"))
        ET.SubElement(rpr, qn("w:bCs"))
    remove_children(rpr, "w:color")
    if color:
        set_attr(get_or_make(rpr, "w:color"), "w:val", color)


def set_paragraph_runs_font(p, font, size_pt, bold=False, color=None):
    """整段刷成同一字体（正文、标题、红头用）。段落标记 rPr 一起刷，
    空段落也能保持行高。"""
    half = pt_to_half_points(size_pt)
    for r in p.findall(qn("w:r")):
        apply_run_props(get_or_make(r, "w:rPr"), font, half, bold, color)
    ppr = get_or_make(p, "w:pPr")
    apply_run_props(get_or_make(ppr, "w:rPr"), font, half, bold, color)


def _make_run(text, font, size_half_points, bold=False, color=None):
    r = ET.Element(qn("w:r"))
    apply_run_props(get_or_make(r, "w:rPr"), font, size_half_points, bold, color)
    t = ET.SubElement(r, qn("w:t"))
    t.set("{http://www.w3.org/XML/1998/namespace}space", "preserve")
    t.text = text
    return r


def set_label_value_runs(p, label, value, label_spec, value_spec):
    """把段落重建为「标签（黑体）+ 值（仿宋）」两段 run。

    这是兄弟 skill 的 core 做不到的能力 —— 它的 set_paragraph_runs_font()
    会把整段刷成一个字体，而信息栏必须标签黑体、值仿宋_GB2312。

    label 需自带全角冒号（调用方用 pad_label() 补齐后传入）。
    只清 w:r，保留 pPr 与书签等其他子元素。
    """
    remove_children(p, "w:r")
    ppr = p.find(qn("w:pPr"))
    at = list(p).index(ppr) + 1 if ppr is not None else 0

    p.insert(at, _make_run(label, label_spec["font"],
                           pt_to_half_points(label_spec["size_pt"]),
                           label_spec.get("bold", False)))
    if value:
        p.insert(at + 1, _make_run(value, value_spec["font"],
                                   pt_to_half_points(value_spec["size_pt"]),
                                   value_spec.get("bold", False)))
    # 段落标记随值的字体，接着打字时延续正文体例
    apply_run_props(get_or_make(get_or_make(p, "w:pPr"), "w:rPr"),
                    value_spec["font"],
                    pt_to_half_points(value_spec["size_pt"]),
                    value_spec.get("bold", False))


def set_two_part_runs(p, head, rest, head_spec, rest_spec):
    """把段落重建为「标题（加粗，标题字体）+ 说明（正文字体）」两段 run。

    用于「（一）登录认证演示。演示了……」这类标题与说明合排的段落 ——
    整段刷标题字体会让一大段说明文字都变楷体，观感不对。

    rest 里的 **加粗** 标记会被识别，加粗部分用 rest_spec 字体加粗。
    """
    for r in list(p.findall(qn("w:r"))):
        if not has_drawing(r):
            p.remove(r)
    ppr = p.find(qn("w:pPr"))
    at = list(p).index(ppr) + 1 if ppr is not None else 0
    p.insert(at, _make_run(head, head_spec["font"],
                           pt_to_half_points(head_spec["size_pt"]),
                           head_spec.get("bold", False)))

    # rest 可能含 **加粗** 标记
    segments = parse_bold_segments(rest)
    if len(segments) <= 1 and segments and not segments[0][1]:
        # 无加粗标记，退化为单 run
        p.insert(at + 1, _make_run(rest, rest_spec["font"],
                                   pt_to_half_points(rest_spec["size_pt"]),
                                   rest_spec.get("bold", False)))
    else:
        insert_at = at + 1
        half = pt_to_half_points(rest_spec["size_pt"])
        for seg_text, is_bold in segments:
            bold = rest_spec.get("bold", False) or is_bold
            p.insert(insert_at, _make_run(seg_text, rest_spec["font"], half, bold))
            insert_at += 1

    apply_run_props(get_or_make(get_or_make(p, "w:pPr"), "w:rPr"),
                    rest_spec["font"], pt_to_half_points(rest_spec["size_pt"]),
                    rest_spec.get("bold", False))


def parse_bold_segments(text):
    """把 Markdown **加粗**标记拆成 [(text, is_bold), …] 段。

    「**能力指标依托资质证明。**」→ [('能力指标依托资质证明。', True)]
    「说明文字。**结论。**后续。」→ [('说明文字。', False), ('结论。', True), ('后续。', False)]

    不成对的 ** 原样保留（当作普通文字，不删除不标记）。
    """
    # 先检查是否成对：** 数量必须是偶数
    count = text.count("**")
    if count % 2 != 0:
        # 不成对，原样返回
        return [(text, False)]

    segments = []
    parts = text.split("**")
    # parts[0] before first **, parts[1] inside first bold, parts[2] after, …
    # Even indices = normal, odd indices = bold
    for i, part in enumerate(parts):
        if not part:
            continue
        segments.append((part, i % 2 == 1))
    return segments


def set_bold_marked_runs(p, text, base_spec):
    """按 **加粗** 标记把段落重建为多段 run，加粗部分用 base_spec 字体加粗。

    无 ** 标记时退化为整段刷 base_spec 字体 + bold。
    """
    segments = parse_bold_segments(text)
    if not segments:
        return
    # 只有一个非粗段 → 整段刷（退化为原逻辑）
    if len(segments) == 1 and not segments[0][1]:
        set_paragraph_runs_font(p, base_spec["font"], base_spec["size_pt"],
                                base_spec.get("bold", False),
                                base_spec.get("color"))
        return

    # 多段：清除旧 run，逐段插入
    for r in list(p.findall(qn("w:r"))):
        if not has_drawing(r):
            p.remove(r)
    ppr = p.find(qn("w:pPr"))
    at = list(p).index(ppr) + 1 if ppr is not None else 0

    half = pt_to_half_points(base_spec["size_pt"])
    for seg_text, is_bold in segments:
        bold = base_spec.get("bold", False) or is_bold
        r = _make_run(seg_text, base_spec["font"], half, bold,
                      base_spec.get("color"))
        p.insert(at, r)
        at += 1

    # 段落标记随 base 字体
    apply_run_props(get_or_make(get_or_make(p, "w:pPr"), "w:rPr"),
                    base_spec["font"], half,
                    base_spec.get("bold", False),
                    base_spec.get("color"))


def set_paragraph_text(p, text, spec):
    """整段替换文字为单一 run。

    只清有文字的 run —— 带图片/图形的 run 保留。红头第二段锚着那条红线，
    连它一起清掉会把红线弄丢。
    """
    for r in list(p.findall(qn("w:r"))):
        if not has_drawing(r):
            p.remove(r)
    ppr = p.find(qn("w:pPr"))
    at = list(p).index(ppr) + 1 if ppr is not None else 0
    p.insert(at, _make_run(text, spec["font"],
                           pt_to_half_points(spec["size_pt"]),
                           spec.get("bold", False), spec.get("color")))


# ---------------- 标签补齐 ----------------
def han_width(text):
    """按汉字宽计文本宽度：汉字/全角 = 1，ASCII 空格 = 1/4。

    模板的信息栏标签全部补到 4 个汉字宽，这是从模板逐项核对出来的规律：
    「时」+8空格+「间」、「主」+2空格+「持」+2空格+「人」、「出席人员」+0空格。
    """
    w = 0.0
    for ch in text:
        if ch == " ":
            w += 0.25
        elif ord(ch) > 0x2E80:  # CJK 及全角
            w += 1.0
        else:
            w += 0.5
        # 半角英数按半宽计，标签里正常不出现
    return w


def pad_label(label_chars, width_han=4):
    """把纯标签文字（不含冒号）用 ASCII 空格均匀补齐到 width_han 个汉字宽。

    2 字 -> 「时」+8空格+「间」；3 字 -> 「主」+2空格+「持」+2空格+「人」；
    4 字 -> 原样。补不进去（超宽）时原样返回。
    """
    chars = list(label_chars)
    n = len(chars)
    if n < 2:
        return label_chars
    need = width_han - n           # 还差多少汉字宽
    if need <= 0:
        return label_chars
    total_spaces = int(round(need * 4))   # 每个汉字宽 = 4 个空格
    gaps = n - 1
    per, extra = divmod(total_spaces, gaps)
    out = []
    for i, ch in enumerate(chars):
        out.append(ch)
        if i < gaps:
            out.append(" " * (per + (1 if i < extra else 0)))
    return "".join(out)


# ---------------- 节属性 ----------------
def iter_sectPr(root):
    return list(root.iter(qn("w:sectPr")))


def read_section(sectpr):
    out = {"pgW": None, "pgH": None, "top": None, "bottom": None,
           "left": None, "right": None, "header": None, "footer": None}
    sz = sectpr.find(qn("w:pgSz"))
    if sz is not None:
        out["pgW"] = _int_or_none(get_attr(sz, "w:w"))
        out["pgH"] = _int_or_none(get_attr(sz, "w:h"))
    mar = sectpr.find(qn("w:pgMar"))
    if mar is not None:
        for k in ("top", "bottom", "left", "right", "header", "footer"):
            out[k] = _int_or_none(get_attr(mar, "w:" + k))
    return out


def set_page_size(sectpr, w_twips, h_twips):
    pgsz = get_or_make(sectpr, "w:pgSz")
    set_attr(pgsz, "w:w", w_twips)
    set_attr(pgsz, "w:h", h_twips)


def set_page_margins(sectpr, top, bottom, left, right, header=708, footer=708):
    mar = get_or_make(sectpr, "w:pgMar")
    for k, v in (("top", top), ("bottom", bottom), ("left", left),
                 ("right", right), ("header", header), ("footer", footer)):
        set_attr(mar, "w:" + k, v)
    set_attr(mar, "w:gutter", 0)


# ---------------- 段落构造 ----------------
def new_paragraph():
    p = ET.Element(qn("w:p"))
    ET.SubElement(p, qn("w:pPr"))
    return p


# ---------------- 包读写 ----------------
def read_parts(path):
    """返回 (有序名单, {名: bytes}, {名: ZipInfo})。"""
    names, data, infos = [], {}, {}
    with zipfile.ZipFile(path, "r") as z:
        for info in z.infolist():
            names.append(info.filename)
            data[info.filename] = z.read(info.filename)
            infos[info.filename] = info
    return names, data, infos


def write_parts(path, names, data, infos):
    """按原顺序重打包，其余部件（图片、表格、批注）字节级保留。"""
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
        for name in names:
            zi = zipfile.ZipInfo(name, date_time=infos[name].date_time)
            zi.compress_type = zipfile.ZIP_DEFLATED
            zi.external_attr = infos[name].external_attr
            z.writestr(zi, data[name])


def validate_docx(path):
    """产出后自检，返回警告列表（空 = 正常）。"""
    warnings = []
    try:
        with zipfile.ZipFile(path, "r") as z:
            bad = z.testzip()
            if bad:
                warnings.append("部件损坏：%s" % bad)
            for need in ("[Content_Types].xml", DOC_PART):
                if need not in z.namelist():
                    warnings.append("缺少 %s" % need)
            if DOC_PART in z.namelist():
                raw = z.read(DOC_PART)
                try:
                    ET.fromstring(raw)
                except ET.ParseError as e:
                    warnings.append("document.xml 解析失败：%s" % e)
                # 前缀漏声明时 XML 仍能解析、zip 也完好，但 Word 拒绝打开。
                # 只查前两项漏过一次，这条必须一起查。
                warnings.extend(check_namespaces(raw))
    except zipfile.BadZipFile as e:
        warnings.append("不是有效的 zip：%s" % e)
    return warnings


def split_root_tag(xml_bytes):
    """从原始字节里切出 <w:document …> 那个起始标签。取不到返回 None。"""
    text = xml_bytes.decode("utf-8", "replace")
    m = re.search(r"<[\w:]*document\b[^>]*?>", text)
    return m.group(0) if m else None


def _declared_prefixes(tag):
    return dict(re.findall(r'xmlns:([\w\d]+)="([^"]*)"', tag))


def merge_root_ns(orig_tag, new_tag):
    """以原始根标签为准，补上 ET 新增的声明（按 URI 比对，不重复）。

    只有原件没声明过的 URI 才补 —— 原件的前缀名要原样保住，
    `mc:Ignorable` 里点的就是那些名字。
    """
    have = set(_declared_prefixes(orig_tag).values())
    add = ["", ]
    for prefix, uri in _declared_prefixes(new_tag).items():
        if uri not in have:
            add.append('xmlns:%s="%s"' % (prefix, uri))
            have.add(uri)
    if len(add) == 1:
        return orig_tag
    return orig_tag[:-1].rstrip() + " ".join(add) + orig_tag[-1]


def check_namespaces(xml_bytes):
    """根标签是否声明了所有被用到的前缀 + Ignorable 里点到的前缀。

    这是写盘前的硬性检查：漏声明时 XML 仍合法可解析、zip 也完好，
    但 Word 会拒绝打开并报「文件可能已经损坏」。
    """
    text = xml_bytes.decode("utf-8", "replace")
    tag = split_root_tag(xml_bytes)
    if not tag:
        return ["取不到根起始标签"]
    declared = set(_declared_prefixes(tag))
    used = set(re.findall(r"</?([\w\d]+):", text))
    used |= set(re.findall(r'\s([\w\d]+):[\w\d]+=', text))
    used -= {"xmlns", "xml"}
    ign = re.search(r'[\w\d]+:Ignorable="([^"]*)"', tag)
    if ign:
        used |= set(ign.group(1).split())
    missing = sorted(used - declared)
    return ["前缀被用到却没在根标签声明：%s" % " ".join(missing)] if missing else []


def load_document(path):
    """打开 docx，返回 (root, body, names, data, infos)。"""
    register()
    names, data, infos = read_parts(path)
    if DOC_PART not in data:
        raise ValueError("不是有效的 Word 文档：缺少 %s" % DOC_PART)
    root = ET.fromstring(data[DOC_PART])
    orig = split_root_tag(data[DOC_PART])
    if orig:
        _ORIG_ROOT_TAG[id(root)] = orig
    body = root.find(qn("w:body"))
    if body is None:
        raise ValueError("%s 里没有 <w:body>" % DOC_PART)
    return root, body, names, data, infos


def top_level_paragraphs(body):
    """只取 body 直属段落（不含表格内），角色识别按这个序列走。"""
    return body.findall(qn("w:p"))


def serialize(root):
    """回写 document.xml。把 ET 生成的根标签换回原始那个。

    不换的话 ET 重写过的前缀（ns1/ns2…）与根标签上
    mc:Ignorable="w14 w15 wp14" 里的名字对不上，那些前缀失去声明，
    Word 拒绝打开报「文件可能已经损坏」—— XML 却仍然合法可解析。
    """
    out = ET.tostring(root, encoding="UTF-8", xml_declaration=True)
    orig = _ORIG_ROOT_TAG.get(id(root))
    if not orig:
        return out
    new_tag = split_root_tag(out)
    if not new_tag:
        return out
    merged = merge_root_ns(orig, new_tag)
    return out.replace(new_tag.encode("utf-8"), merged.encode("utf-8"), 1)
