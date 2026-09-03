#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""国家开放大学会议纪要格式规格 + 段落角色识别。

所有数值直接从模板 assets/国家开放大学会议纪要模板.docx 的
word/document.xml 提取核对，不采信模板正文里的文字标注。

两处已知的模板自相矛盾，以 XML 为准：
1. 模板写"一级标题黑体小三"，XML 实际 sz=32（三号 16pt），不是小三 15pt。
2. 模板结尾有 1 个 <w:drawing> 但 zip 内无 media 部件 —— 是断链的图片占位。
"""
import re

# ---------------- 字体 ----------------
FANGSONG = "仿宋_GB2312"
KAITI = "楷体_GB2312"
HEITI = "黑体"
XIAOBIAOSONG = "方正小标宋简体"
WESTERN = "Times New Roman"

# 本机常缺的字体：写入的名字是对的，只是预览会被替换
FONTS_MAY_MISSING = (XIAOBIAOSONG,)

# ---------------- 页面 ----------------
A4_W = 11906
A4_H = 16838
PAGE_MARGIN_CM = 2.54
PAGE_HEADER_TWIPS = 708
PAGE_FOOTER_TWIPS = 708

# ---------------- 行距 ----------------
LINE_BODY_PT = 28      # line=560 固定值，正文及以下全部
LINE_REDHEAD_PT = 30   # line=600 固定值，仅红头两行

# ---------------- 角色规格 ----------------
# size_pt 是整磅；写入时换算成半磅。
REDHEAD = {"font": HEITI, "size_pt": 26, "bold": True, "color": "FF0000",
           "align": "center", "line_pt": LINE_REDHEAD_PT, "after_pt": 12}
TITLE = {"font": XIAOBIAOSONG, "size_pt": 18, "bold": False,
         "align": "center", "line_pt": LINE_BODY_PT}
INFO_LABEL = {"font": HEITI, "size_pt": 16, "bold": False}
INFO_VALUE = {"font": FANGSONG, "size_pt": 16, "bold": False}
BODY = {"font": FANGSONG, "size_pt": 16, "bold": False,
        "align": "both", "line_pt": LINE_BODY_PT, "indent_chars": 200}
H1 = {"font": HEITI, "size_pt": 16, "bold": True,
      "align": "both", "line_pt": LINE_BODY_PT, "indent_chars": 200}
H2 = {"font": KAITI, "size_pt": 16, "bold": True,
      "align": "both", "line_pt": LINE_BODY_PT, "indent_chars": 200}
H3 = dict(BODY)   # 三级标题及以下与正文同体例
H4 = dict(BODY)

# 照片段：inline 图片的高度由图片自己决定，固定行距会把图裁掉 ——
# lineRule="exact" 是硬裁剪，Word 不会为图片长高。11.92cm 的照片
# 碰上 28 磅固定行距（0.99cm）只剩顶上一条，裁掉 92%。
# 实测 33 份真实纪要的照片段行距全部是 auto，没有一份用 exact。
# 缩进也必须归 0：照片宽 15.89cm，文本栏 15.92cm，再加 2 字符缩进
# （1.13cm）就溢出 1.10cm。
PHOTO = {"font": FANGSONG, "size_pt": 16, "bold": False,
         "align": "center", "line_pt": None, "indent_chars": 0}

SPEC_BY_ROLE = {
    "redhead": REDHEAD, "title": TITLE, "body": BODY, "lead": BODY,
    "h1": H1, "h2": H2, "h3": H3, "h4": H4, "photo": PHOTO,
}

# ---------------- 信息栏 ----------------
# 顺序固定。label 是纯标签文字（不含冒号），required 决定缺失时的严重级别。
INFO_FIELDS = [
    {"key": "time",     "label": "时间",   "required": True,
     "hint": "如 2026年8月6日（周四）下午14:00"},
    {"key": "place",    "label": "地点",   "required": True,
     "hint": "如 五棵松A901会议室"},
    {"key": "host",     "label": "主持人", "required": True, "hint": "姓名"},
    {"key": "attendees", "label": "出席人员", "required": True,
     "hint": "按部门分行，如「信息化部：张三、李四」"},
    {"key": "recorder", "label": "记录人", "required": True, "hint": "姓名"},
    {"key": "topic",    "label": "会议议题", "required": True, "hint": "一句话议题"},
    {"key": "content",  "label": "会议内容", "required": True,
     "hint": "固定为「会议内容：」，其后接帽子段与正文"},
]
INFO_BY_KEY = {f["key"]: f for f in INFO_FIELDS}
INFO_LABEL_WIDTH_HAN = 4   # 标签统一补齐到 4 个汉字宽

# 不属于信息栏、但 --fill 也要接的键，报告里按中文名显示
EXTRA_FILL_LABELS = {
    "meeting_name": "红头会议名称",
    "title": "纪要标题",
    "lead": "帽子段",
}


def fill_label(key):
    f = INFO_BY_KEY.get(key)
    if f:
        return f["label"]
    return EXTRA_FILL_LABELS.get(key, key)

# 「出席人员：」下方按部门分行，这些行是值行而非标签行。
# 允许标签内的填充空格（模板里有「教  务  部：赵东伟」这种补齐写法）。
_RE_DEPT_LINE = re.compile(r"^\s*[^：:\s](?:[^：:]{0,14})[：:]\s*\S")

# ---------------- 红头 ----------------
REDHEAD_ORG = "国家开放大学"
REDHEAD_SUFFIX = "会议纪要"
REDHEAD_LINES = 2   # 红头固定两行，多了就是把标题误吞进来了
# 红头判定**必须锚定第一行等于「国家开放大学」**，不能用「或以会议纪要结尾」
# 当或条件 —— 中文纪要的标题本身几乎总以「会议纪要」结尾（「XX周例会会议纪要」），
# 拿它当或条件会把没有红头的文件的标题误认成红头。
# 实测 36 份真实纪要：旧的或条件判错 30 份，只判对 6 份。
# 误判的连带后果是红线锚到标题上，见 mj_docx.set_redline_border 的调用方。
# 第二行形如「"一平台"项目会议纪要」——项目名可变，尾部固定「会议纪要」

# 红线：红头下方的红色横线。公文惯例，必备。
#
# 模板里是锚定在红头第二段的浮动 line 形状（FF0000、宽 38100 EMU = 3pt、
# 长 5734050 EMU），带 5498 字符的 VML 兜底。脚本改用**段落下边框**生成：
#   文本栏宽 = 21cm - 2.54×2 = 15.92cm，模板线长 5734050 EMU = 15.93cm，
#   差 0.01cm —— 视觉等价，但下边框只要一行 XML，不需要 drawing 部件、
#   不需要 rels 与 Content_Types 条目，也不会像浮动图形那样被编辑时挪位。
# 校验两种都认（见 mj_docx.find_redline）。
REDLINE_COLOR = "FF0000"
REDLINE_WIDTH_EMU = 38100      # 3pt，浮动形状的 a:ln/@w
REDLINE_BORDER_SZ = 24         # 3pt，边框 w:sz 用 1/8 磅
REDLINE_BORDER_SPACE = 8       # 文字与线的间距（磅），贴近模板的观感
REDLINE_LENGTH_EMU = 5734050

# ---------------- 标题层级正则 ----------------
RE_H1 = re.compile(r"^\s*[一二三四五六七八九十百]+\s*[、.]")     # 一、
RE_H2 = re.compile(r"^\s*[（(][一二三四五六七八九十]+[)）]")      # （一）
RE_H3 = re.compile(r"^\s*\d+\s*[.．、]\s*\S")                     # 1.
RE_H4 = re.compile(r"^\s*[（(]\d+[)）]")                          # （1）


def heading_role(text):
    """文本自带编号时返回 h1..h4，没编号返回 None。

    `（1）` 必须在 `一、` 之前判，否则会被 RE_H1 里的数字规则抢走。
    校验器与 md 入口都走这一个判定 —— 之前 build_from_text 把手写编号的段
    当 body 排（仿宋不加粗），校验器却按 h2 判，同一份产出自己报 F1。
    """
    t = text.strip()
    if RE_H4.match(t):
        return "h4"
    if RE_H2.match(t):
        return "h2"
    if RE_H1.match(t):
        return "h1"
    if RE_H3.match(t):
        return "h3"
    return None

# 「（一）登录认证演示。演示了……」这种标题与说明写在同一段的情况。
# 标题部分 = 编号 + 短语 + 收尾标点；其后是正文说明。
# 只认第一个句号/分号/冒号，且标题部分不超过 HEADING_INLINE_MAX 字 ——
# 太长就不是标题合排，而是一整段正文，整段按标题体例更稳。
HEADING_INLINE_MAX = 30
_RE_INLINE_SPLIT = re.compile(r"^(\s*(?:[（(][一二三四五六七八九十\d]+[)）]"
                              r"|[一二三四五六七八九十百]+\s*[、.]"
                              r"|\d+\s*[.．、])\s*[^。；：!?！？]{0,%d}[。；：])(.+)$"
                              % HEADING_INLINE_MAX)


def split_inline_heading(text):
    """把「（一）标题。说明文字」拆成 (标题含标点, 说明)。

    不是这种合排结构时返回 None —— 纯标题行、纯正文都走原来的整段处理。
    """
    m = _RE_INLINE_SPLIT.match(text)
    if not m:
        return None
    head, rest = m.group(1), m.group(2).strip()
    if not rest:
        return None
    return head, rest
RE_NUMBERED = re.compile(
    r"^\s*(?:[一二三四五六七八九十百]+\s*[、.]|[（(][一二三四五六七八九十]+[)）]"
    r"|\d+\s*[.．、]|[（(]\d+[)）])")


def is_blank(text):
    return not text.strip()


def match_info_label(text):
    """文本是否以某个信息栏标签开头。返回 field dict 或 None。

    容忍标签中间的填充空格（时  间：）与半角冒号。
    """
    t = text.strip()
    if "：" not in t and ":" not in t:
        return None
    head = re.split(r"[：:]", t, 1)[0]
    compact = re.sub(r"\s+", "", head)
    for f in INFO_FIELDS:
        if compact == f["label"]:
            return f
    return None


def looks_like_dept_line(text):
    """「信息化部：任冉、杨亚菲」这类出席人员的部门行。"""
    t = text.strip()
    if match_info_label(t):
        return False
    return bool(_RE_DEPT_LINE.match(t))


def classify_all(texts, paragraphs=None):
    """给全篇 top-level 段落定角色。

    返回与 texts 等长的角色列表，取值：
      redhead / title / info / dept / lead / h1 / h2 / h3 / h4 / body /
      photo / blank

    保守原则（沿用兄弟 skill）：认不出就归 body，绝不删字或重排。
    """
    n = len(texts)
    roles = ["body"] * n
    non_blank = [i for i, t in enumerate(texts) if not is_blank(t)]

    # --- 红头：必须以「国家开放大学」开头 ---
    # 第一个非空段不等于「国家开放大学」就判**全篇无红头**，那些段落留给
    # 下面的 title 规则。不能拿「以会议纪要结尾」当或条件 —— 见 REDHEAD_LINES
    # 处的注释，纪要标题本身几乎总以「会议纪要」结尾，会被误认成红头。
    redhead_idx = []
    if non_blank and texts[non_blank[0]].strip() == REDHEAD_ORG:
        redhead_idx.append(non_blank[0])
        # 第二行是「会议名 + 会议纪要」，最多到 REDHEAD_LINES 行
        for i in non_blank[1:REDHEAD_LINES]:
            if texts[i].strip().endswith(REDHEAD_SUFFIX):
                redhead_idx.append(i)
            break
    for i in redhead_idx:
        roles[i] = "redhead"

    # --- 信息栏 + 部门行 ---
    first_info = None
    last_info = None
    for i in non_blank:
        if roles[i] == "redhead":
            continue
        if match_info_label(texts[i]):
            roles[i] = "info"
            if first_info is None:
                first_info = i
            last_info = i

    # 出席人员之后、下一个 info 之前的部门行
    if first_info is not None:
        for i in non_blank:
            if first_info < i < (last_info or first_info) and roles[i] == "body" \
                    and looks_like_dept_line(texts[i]):
                roles[i] = "dept"

    # --- 标题：红头之后、第一个信息栏之前的非空段 ---
    limit = first_info if first_info is not None else n
    for i in non_blank:
        if i >= limit:
            break
        if roles[i] == "body":
            roles[i] = "title"

    # --- 「会议内容：」之后：帽子段 + 标题层级 + 正文 ---
    content_idx = next(
        (i for i in non_blank
         if roles[i] == "info" and match_info_label(texts[i])["key"] == "content"),
        None)
    body_start = (content_idx + 1) if content_idx is not None else \
        ((last_info + 1) if last_info is not None else n)

    lead_done = False
    for i in range(body_start, n):
        if is_blank(texts[i]):
            roles[i] = "blank"
            continue
        if roles[i] != "body":
            continue
        t = texts[i].strip()
        hr = heading_role(t)
        if hr:
            roles[i] = hr
        elif not lead_done:
            roles[i] = "lead"       # 帽子段：会议内容后第一个无编号段
            lead_done = True
        else:
            roles[i] = "body"
        if roles[i] in ("h1", "h2", "h3", "h4"):
            lead_done = True        # 直接进标题说明没写帽子段

    # --- 照片段与空行 ---
    # 带真图片的段落是照片段（红线是线条形状，不算图片，已由 has_image 区分）
    if paragraphs is not None:
        from mj_docx import has_image
        for i in range(n):
            if has_image(paragraphs[i]):
                roles[i] = "photo"
    for i, t in enumerate(texts):
        if is_blank(t) and roles[i] != "photo":
            roles[i] = "blank"

    return roles


def spec_for(role):
    """角色 -> 格式规格。info/dept/blank 不在此表，另行处理。"""
    return SPEC_BY_ROLE.get(role)


def heading_level(role):
    return {"h1": 1, "h2": 2, "h3": 3, "h4": 4}.get(role)
