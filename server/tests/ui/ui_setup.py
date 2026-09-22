"""M6 浏览器验收的数据准备。

幂等：账号固定，重复跑不会重复建；用来把「AI 驱动浏览器」与「造数据」解耦——
浏览器只负责点 UI，数据一律走接口准备，免得脚本里塞一堆注册表单交互。

用法（任意 CWD 均可）:
    cd server && python tests/ui/ui_setup.py setup
    python server/tests/ui/ui_setup.py setup

命令:
    setup   建家长(含 PIN)+孩子，护眼/时长恢复测试默认值（幂等，不动已有的孩子）
    fresh   ⚠️ 归档该账号下**现有全部孩子**，重建一个同名「哥哥」，护眼恢复测试默认值
    rest1   rest_interval_min=1、session_limit_min=5（测 20-20-20 休息页）
    lock1   rest_interval_min=0、session_limit_min=5（测单次到点锁屏）
    reset   同 setup（恢复测试默认值）

关于 fresh：方向键类流程（flow1/3/5/7）依赖「当日组卷里有多选项题」，而多选项需要同一
会话里有多个知识点当干扰项。反复跑会把「新学」池吃光、当天只剩寥寥几道题，甚至退化成
单选项题（方向键无处可移）。fresh 通过「归档旧孩子 + 重建同名新孩子」把学习状态清零，
让组卷回到满池。**它会归档当前账号下所有孩子，只对测试账号用。**

关于 daily_limit_min：一律给到上限 480。原因是后端 TodayUsedSeconds 对「已开始
但没结束」的会话按 `now - started_at` 计，而当前没有会话过期回收，于是反复跑同一
天的验收会不断把当日时长顶高；一旦 remaining<=0，组卷就不再排新题、会话可能起不来，
护眼场景会被日额度掩盖。给到上限即「日额度不设闸门」，让浏览器流程专注验护眼；
日额度本身的文案由后端 planMessage 承担，不在浏览器流程里验。

账号与 PIN 一律从 server/.env 读（SMOKE_ACCOUNT / SMOKE_PASSWORD / SMOKE_PIN），
脚本里不出现明文——server/.env 已 gitignore，仓库只提交 .env.example 占位值。
"""
import json
import os
import sys
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from _common import env  # noqa: E402

API = env("API_BASE_URL", "http://127.0.0.1:18080/api/v1")
EMAIL = env("SMOKE_ACCOUNT")
PASSWORD = env("SMOKE_PASSWORD")
PIN = env("SMOKE_PIN")

# 测试默认值：日额度给到上限，等价于「不设闸门」（理由见文件头 docstring）。
DAILY_UNLIMITED = 480
# 流程脚本按名字点孩子，所以固定用这个名字；重建后仍是同一个名字。
KID = {"nickname": "哥哥", "avatar_id": "panda", "birth_ym": "202001", "stage_code": "S1"}


def apply_settings(token, **fields):
    """改家长设置，返回 (status, payload)。"""
    st, p = call("PUT", "/parent/settings", fields, token=token)
    print(f"设置 {fields} -> {st} {(p.get('data') or p.get('error') or '')}")
    return st, p


def list_children(token):
    st, p = call("GET", "/children", token=token)
    return p.get("data") or []


def ensure_child(token):
    """有孩子就复用（幂等）；一个都没有才建。"""
    kids = list_children(token)
    if kids:
        return kids
    st, p = call("POST", "/children", KID, token=token)
    print(f"建孩子 -> {st} {(p.get('data') or {}).get('id')}")
    return [p.get("data") or {}]


def fresh_child(token):
    """归档现有全部孩子，重建一个同名「哥哥」——把学习状态清零。

    ⚠️ 会归档该账号现有的所有孩子，只对测试账号（SMOKE_ACCOUNT）用。
    """
    for kid in list_children(token):
        st, p = call("DELETE", "/children/" + str(kid.get("id")), token=token)
        print(f"归档孩子 {kid.get('nickname')} -> {st} {(p.get('data') or p.get('error') or '')}")
    st, p = call("POST", "/children", KID, token=token)
    print(f"重建孩子 -> {st} {(p.get('data') or {}).get('id')}")

OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def call(method, path, body=None, token=None):
    req = urllib.request.Request(API + path, method=method)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    payload = None
    if body is not None:
        payload = json.dumps(body, ensure_ascii=False).encode()
        req.add_header("Content-Type", "application/json")
    try:
        with OPENER.open(req, data=payload, timeout=60) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except Exception:  # noqa: BLE001
            return e.code, {}


def login_or_register():
    st, p = call("POST", "/auth/login", {"account": EMAIL, "password": PASSWORD})
    if st != 200:
        st, p = call("POST", "/auth/register", {
            "email": EMAIL, "password": PASSWORD,
            "display_name": "妈妈", "invite_code": env("BOOTSTRAP_INVITE_CODE"),
        })
        if st not in (200, 201):
            raise SystemExit(f"注册失败 {st}: {p}")
    return (p.get("data") or {}).get("access_token")


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else "setup"
    token = login_or_register()
    if not token:
        raise SystemExit("拿不到 access_token")

    st, p = call("PUT", "/auth/pin", {"pin": PIN}, token=token)
    print(f"设置 PIN -> {st}")

    if cmd in ("setup", "reset"):
        apply_settings(token, rest_interval_min=20, session_limit_min=20,
                       daily_limit_min=DAILY_UNLIMITED)
        print("孩子数:", len(ensure_child(token)))
    elif cmd == "fresh":
        # ⚠️ 会归档该账号现有全部孩子（只对测试账号用），把学习状态清零。
        apply_settings(token, rest_interval_min=20, session_limit_min=20,
                       daily_limit_min=DAILY_UNLIMITED)
        fresh_child(token)
    elif cmd == "rest1":
        # 注意：单次时长后端只收 5–120（0 不合法），休息间隔才是 0–120。
        # 所以测休息页时把休息间隔压到 1 分钟、单次时长给最小值 5，休息必然先触发。
        apply_settings(token, rest_interval_min=1, session_limit_min=5,
                       daily_limit_min=DAILY_UNLIMITED)
    elif cmd == "lock1":
        # 反过来：关掉休息页、把单次时长压到最小，让「到点锁屏」先触发。
        apply_settings(token, rest_interval_min=0, session_limit_min=5,
                       daily_limit_min=DAILY_UNLIMITED)
    else:
        raise SystemExit("未知命令 " + cmd)


main()
