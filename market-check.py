# 技能市场：把安装的每一步单独打一遍，看各自返回什么。
import json
import sys
import urllib.request
import urllib.error

TOKEN = ""  # 粘贴一份新的 access_token（2 小时过期）
TENANT = "wjtest"
SKILL_ID = 3
if not sys.stdout.isatty():
    try:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass

BASE = "http://123.249.111.75:18093/api/user"


def call(path, note):
    url = BASE + path
    req = urllib.request.Request(url, headers={
        "Authorization": "Bearer " + TOKEN,
        "X-Selected-Tenant-ID": TENANT,
    })
    print()
    print("=== " + note)
    print("    GET " + url)
    try:
        r = urllib.request.urlopen(req, timeout=20)
        body = r.read()
        print("    " + str(r.status))
    except urllib.error.HTTPError as e:
        body = e.read()
        print("    " + str(e.code))
        # 500 的响应头里常有 request id / 追踪 id，服务端查日志要用
        for k, v in e.headers.items():
            print("    < %s: %s" % (k, v))
    except Exception as e:
        print("    失败 %s" % e)
        return None
    text = body.decode("utf-8", "replace")
    print("    " + text[:600])
    try:
        return json.loads(text)
    except Exception:
        return None


# ① 列表：确认令牌和租户是通的
call("/skills?visibility=global&size=1", "列表")

# ② 详情：看 has_bundle 是不是 true
d = call("/skills/%d" % SKILL_ID, "详情")
if d:
    print("    -> name=%s has_bundle=%s version=%s" % (d.get("name"), d.get("has_bundle"), d.get("version")))

# ③ 版本历史：能看到 bundle_uri 与 sha256，说明包在对象存储里是有记录的
call("/skills/%d/versions" % SKILL_ID, "版本历史")

# ④ 下载：预签名链接。这一步现在 500
dl = call("/skills/%d/download" % SKILL_ID, "下载链接")
call("/skills/%d/download?expires=600" % SKILL_ID, "下载链接（带 expires=600）")

# ⑤ 真拿到链接了就去下一下，看对象存储那边通不通
if dl and dl.get("url"):
    print()
    print("=== 下载实体")
    print("    GET " + dl["url"][:120])
    try:
        r = urllib.request.urlopen(dl["url"], timeout=60)
        data = r.read()
        print("    %s  %d 字节  期望 sha256=%s" % (r.status, len(data), dl.get("sha256")))
    except urllib.error.HTTPError as e:
        print("    %s %s" % (e.code, e.read()[:300]))
    except Exception as e:
        print("    失败 %s" % e)
