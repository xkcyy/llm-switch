#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""把 LLM Switch 的模型同步到 Trae SOLO（TRAE SOLO CN）的自定义模型列表。

原理
----
TRAE SOLO CN 是 VS Code 分支（Electron），官方支持在 %USERPROFILE%\\.trae-cn\\argv.json
里写 `remote-debugging-port`。本脚本通过 CDP 连上正在运行的 Trae，用 Playwright 走
「模型选择器 → 添加模型 → 设置/模型 → + 添加模型 → 自定义模型」这段官方 UI 流程，
最终提交给服务端注册（对应协议请求 chat/add_custom_model）。

因此：不改数据库、不改本地配置、不需要重新登录；与手工点一遍完全等价。

已验证的关键事实（2026-09，TRAE SOLO CN 1.107.1）
------------------------------------------------
1. Trae 发往自定义模型地址时，请求体里的 ``model`` 就是「模型 ID」本身
   （不是显示名）——所以模型 ID 必须写成 LLM Switch 认识的 `供应商ID/模型ID`。
2. 「完整 URL」开关默认关闭，此时 Trae 会自动在地址后补 `/chat/completions`，
   所以地址填 `http://127.0.0.1:8317/v1` 即可（不要带 /chat/completions）。
3. 提交时会先做一次真实连通性测试，成功的提示是「测试通过，模型保存成功」。
   这次测试会打到本机代理，日志里能看到 `entry=chat model=<模型ID> status=200`。
4. 自定义模型 `client_connect = true`，只在本地环境使用、由本机直连地址。

用法
----
    python sync_models.py --dry-run          # 只对比，不动 Trae
    python sync_models.py                    # 同步（缺什么补什么，已存在则跳过）
    python sync_models.py --only km/gpt-5.6-sol
    python sync_models.py --configure-argv   # 端口没开时，顺便写入 argv.json（需手动重启 Trae）
    python sync_models.py --restart          # 端口没开时，自动重启 Trae（会中断正在跑的会话）

依赖：Python 3.9+、playwright（`pip install playwright`，无需 `playwright install`，
因为连的是已有的 Electron，不下载浏览器）。
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import re
import shutil
import sqlite3
import subprocess
import sys
import time
import urllib.request
from typing import Dict, List, Optional, Tuple

# ---------------------------------------------------------------------------
# 配置
# ---------------------------------------------------------------------------

DEFAULT_API = "http://127.0.0.1:8317/v1"
DEFAULT_CDP = "http://127.0.0.1:9222"
DEFAULT_API_KEY = "llm-switch-local"

# Trae 里「自定义模型」对应的 provider 前缀（应用内部使用）。
CUSTOM_PROVIDER_PREFIX = "custom_"

# 自定义模型表单的字段定位（中英文各一份，按顺序尝试）。
SEL_MODEL_ID = ['input[placeholder*="模型 ID"]', 'input[placeholder*="Model ID"]']
SEL_DISPLAY = ['input[placeholder*="展示名称"]', 'input[placeholder*="isplay name"]']
SEL_APIKEY = ['input[type="password"]']
SEL_URL = [
    'input[placeholder*="api.openai.com/v1"]',
    'input[placeholder*="https://"]',
]
SEL_SUBMIT = ["button.add-model-connect-button", "button.icd-btn-primary"]
SEL_RESET = ["button.add-model-reset-button"]


def log(msg: str) -> None:
    print(msg, flush=True)


# ---------------------------------------------------------------------------
# 1. LLM Switch 侧：拿模型清单
# ---------------------------------------------------------------------------


def load_llm_switch_config() -> Dict:
    path = os.path.join(os.path.expanduser("~"), ".llm-switch", "config.json")
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return {}


def fetch_models(api_base: str) -> List[str]:
    """从本机代理的 /v1/models 取模型 ID（形如 供应商ID/模型ID）。"""
    url = api_base.rstrip("/") + "/models"
    with urllib.request.urlopen(url, timeout=5) as r:
        data = json.load(r)
    ids = [item["id"] for item in data.get("data", []) if item.get("id")]
    return sorted(ids)


def resolve_api_base(explicit: Optional[str]) -> str:
    if explicit:
        return explicit.rstrip("/")
    cfg = load_llm_switch_config()
    proxy = (cfg.get("settings") or {}).get("proxy") or {}
    host = proxy.get("host") or "127.0.0.1"
    port = proxy.get("port") or 8317
    return f"http://{host}:{port}/v1"


# ---------------------------------------------------------------------------
# 2. Trae 侧：读本地缓存的模型清单（用于判断「已存在」）
# ---------------------------------------------------------------------------


def find_user_data_dir(explicit: Optional[str]) -> Optional[str]:
    if explicit:
        return explicit
    cands = [
        os.path.join(os.environ.get("APPDATA", ""), "TRAE SOLO CN"),
        os.path.join(os.environ.get("APPDATA", ""), "Trae CN"),
        os.path.join(os.environ.get("APPDATA", ""), "Trae"),
    ]
    for c in cands:
        if c and os.path.isdir(os.path.join(c, "User", "globalStorage")):
            return c
    return None


def existing_custom_models(user_data_dir: str) -> List[str]:
    """从 state.vscdb 的 model_list_map 里读出已有的自定义模型 ID。

    这只是本地缓存，用来做幂等判断；真正的注册在服务端，由 UI 流程完成。
    """
    db = os.path.join(user_data_dir, "User", "globalStorage", "state.vscdb")
    if not os.path.isfile(db):
        return []
    ids: List[str] = []
    con = sqlite3.connect(f"file:{db}?mode=ro&immutable=1", uri=True)
    try:
        cur = con.cursor()
        rows = cur.execute(
            "select value from ItemTable where key like '%AI.agent.model.model_list_map'"
        ).fetchall()
        for (value,) in rows:
            raw = value if isinstance(value, str) else value.decode("utf-8", "ignore")
            try:
                doc = json.loads(raw)
            except Exception:
                continue
            for models in doc.values():
                if not isinstance(models, list):
                    continue
                for m in models:
                    if not isinstance(m, dict):
                        continue
                    provider = (m.get("provider") or "")
                    name = (m.get("name") or "")
                    if not provider.startswith(CUSTOM_PROVIDER_PREFIX):
                        continue
                    model_id = name.split("//")[-1] if "//" in name else name
                    if model_id and model_id not in ids:
                        ids.append(model_id)
    finally:
        con.close()
    return ids


# ---------------------------------------------------------------------------
# 3. 调试端口 / 进程
# ---------------------------------------------------------------------------


def cdp_alive(cdp: str) -> bool:
    try:
        with urllib.request.urlopen(cdp.rstrip("/") + "/json/version", timeout=2) as r:
            return r.status == 200
    except Exception:
        return False


def find_trae_exe() -> Optional[str]:
    cands = [
        os.path.join(
            os.environ.get("LOCALAPPDATA", ""),
            "Programs",
            "TRAE SOLO CN",
            "TRAE SOLO CN.exe",
        ),
        os.path.join(
            os.environ.get("LOCALAPPDATA", ""), "Programs", "Trae CN", "Trae CN.exe"
        ),
    ]
    for c in cands:
        if c and os.path.isfile(c):
            return c
    return None


def argv_json_path() -> str:
    return os.path.join(os.path.expanduser("~"), ".trae-cn", "argv.json")


def ensure_debug_port(cdp: str, configure: bool, restart: bool) -> bool:
    """确保 9222 可用；必要时写入 argv.json / 重启 Trae。"""
    if cdp_alive(cdp):
        return True

    port = re.sub(r"\D", "", cdp) or "9222"
    argv = argv_json_path()

    if configure and os.path.isfile(argv):
        with open(argv, encoding="utf-8") as f:
            content = f.read()
        if "remote-debugging-port" not in content:
            shutil.copy2(argv, argv + ".bak-llmswitch")
            i = content.index("{")
            content = (
                content[: i + 1]
                + f'\n\t// 由 LLM Switch 添加：允许本机 CDP 自动化（调试端口 {port}）\n\t"remote-debugging-port": "{port}",'
                + content[i + 1 :]
            )
            with open(argv, "w", encoding="utf-8", newline="") as f:
                f.write(content)
            log(f"已写入调试端口配置：{argv}（备份 {argv}.bak-llmswitch）")
        else:
            log(f"argv.json 里已有 remote-debugging-port，未改动：{argv}")

    if restart:
        exe = find_trae_exe()
        if not exe:
            log("找不到 TRAE SOLO CN.exe，请手动重启")
            return False
        log("正在重启 Trae（会中断正在进行的会话）…")
        try:
            subprocess.run(["taskkill", "/IM", os.path.basename(exe), "/T", "/F"],
                           capture_output=True)
        except Exception:
            pass
        time.sleep(3)
        subprocess.Popen([exe], close_fds=True)
        for _ in range(60):
            time.sleep(1)
            if cdp_alive(cdp):
                return True
        return False

    log("")
    log("调试端口未开启。请任选其一后重跑本脚本：")
    log(f"  1) 在 {argv} 里加上：\"remote-debugging-port\": \"{port}\" 然后重启 Trae")
    log("     （本脚本加 --configure-argv 会自动写，--restart 会自动重启）")
    log("  2) 直接带参数启动 Trae：\"TRAE SOLO CN.exe\" --remote-debugging-port=9222")
    return False


# ---------------------------------------------------------------------------
# 4. UI 自动化
# ---------------------------------------------------------------------------


class TraeUI:
    """封装「添加自定义模型」的 UI 操作。"""

    def __init__(self, page, shots_dir: Optional[str] = None):
        self.page = page
        self.shots_dir = shots_dir
        self._shot_n = 0

    # -- 基础工具 ---------------------------------------------------------
    def shot(self, name: str) -> None:
        if not self.shots_dir:
            return
        os.makedirs(self.shots_dir, exist_ok=True)
        self._shot_n += 1
        path = os.path.join(self.shots_dir, f"{self._shot_n:02d}-{name}.png")
        try:
            cdp = self.page.context.new_cdp_session(self.page)
            res = cdp.send("Page.captureScreenshot", {"format": "png"})
            with open(path, "wb") as f:
                f.write(base64.b64decode(res["data"]))
        except Exception as e:  # 截图失败不影响主流程
            log(f"  [warn] 截图失败：{e}")

    def _real_click(self, x: float, y: float) -> None:
        """Radix 组件只认 pointer 事件，必须用真实鼠标点击。"""
        self.page.mouse.move(x, y)
        time.sleep(0.08)
        self.page.mouse.down()
        time.sleep(0.05)
        self.page.mouse.up()

    def _first(self, selectors: List[str]):
        for sel in selectors:
            loc = self.page.locator(sel)
            if loc.count() and loc.first.is_visible():
                return loc.first
        return None

    def _click_text(self, texts: List[str], near_y: Optional[Tuple[int, int]] = None) -> bool:
        """按文本点击（正则在页面上找最小的可点元素）。"""
        res = self.page.evaluate(
            """(args) => {
              const [texts, nearY] = args;
              let best = null, bestArea = Infinity;
              for (const el of document.querySelectorAll('button,[role=button],div,span')) {
                const t = (el.textContent || '').trim();
                if (!texts.includes(t)) continue;
                const r = el.getBoundingClientRect();
                if (r.width < 10 || r.height < 8) continue;
                if (nearY && (r.y < nearY[0] || r.y > nearY[1])) continue;
                const area = r.width * r.height;
                if (area < bestArea) { bestArea = area; best = r; }
              }
              return best ? [best.x + best.width / 2, best.y + best.height / 2] : null;
            }""",
            [texts, list(near_y) if near_y else None],
        )
        if not res:
            return False
        self._real_click(res[0], res[1])
        return True

    # -- 状态判断 ---------------------------------------------------------
    def has_form(self) -> bool:
        return self._first(SEL_MODEL_ID) is not None

    def has_provider_picker(self) -> bool:
        if self.has_form():
            return False
        return bool(self.page.get_by_text("自定义模型", exact=True).count())

    def _click_text_probe(self, texts: List[str]) -> bool:
        n = self.page.evaluate(
            """(texts) => {
              let n = 0;
              for (const el of document.querySelectorAll('button,[role=button]')) {
                const t = (el.textContent || '').trim();
                if (texts.includes(t)) n++;
              }
              return n;
            }""",
            texts,
        )
        return n > 0

    # -- 导航 -------------------------------------------------------------
    def open_settings_models(self) -> None:
        """从聊天页打开 设置 → 模型。"""
        box = self.page.evaluate(
            """() => {
              const el = document.querySelector('button.core-model-select-trigger');
              if (!el) return null;
              const r = el.getBoundingClientRect();
              return [r.x + r.width / 2, r.y + r.height / 2];
            }"""
        )
        if not box:
            raise RuntimeError("找不到模型选择器（button.core-model-select-trigger）")
        self._real_click(box[0], box[1])
        time.sleep(1.0)

        # 弹层里的「添加模型」
        item = self.page.evaluate(
            """() => {
              const pop = document.querySelector('[data-radix-popper-content-wrapper]');
              if (!pop) return null;
              for (const el of pop.querySelectorAll('*')) {
                if ((el.textContent || '').trim() === '添加模型' && el.children.length <= 2) {
                  const r = el.getBoundingClientRect();
                  if (r.width > 20) return [r.x + r.width / 2, r.y + r.height / 2];
                }
              }
              return null;
            }"""
        )
        if not item:
            raise RuntimeError("模型选择器弹层里找不到「添加模型」")
        self._real_click(item[0], item[1])
        time.sleep(1.2)
        self.page.get_by_text("模型管理", exact=True).first.wait_for(timeout=15000)

    def open_custom_form(self) -> None:
        """确保「自定义模型」表单处于打开状态。"""
        if self.has_form():
            return

        if self.has_provider_picker():
            if not self._click_text(["自定义模型"]):
                raise RuntimeError("供应商列表里找不到「自定义模型」")
            time.sleep(1.2)
            if self.has_form():
                return

        if not self._click_text_probe(["添加模型", "+ 添加模型"]):
            self.open_settings_models()
        # 点「+ 添加模型」
        if not self._click_text(["添加模型", "+ 添加模型"]):
            raise RuntimeError("设置/模型页找不到「添加模型」按钮")
        time.sleep(1.5)

        if not self._click_text(["自定义模型"]):
            raise RuntimeError("供应商列表里找不到「自定义模型」")
        time.sleep(1.2)
        if not self.has_form():
            raise RuntimeError("自定义模型表单没有出现")

    # -- 提示 / 状态 -------------------------------------------------------
    def _read_toast(self) -> str:
        return self.page.evaluate(
            """() => {
              let s = '';
              for (const el of document.querySelectorAll('[class*=toast],[class*=Toast],[role=alert]')) {
                const t = (el.textContent || '').trim();
                if (t) s += t + ' | ';
              }
              return s;
            }"""
        ) or ""

    def _submit_text(self) -> str:
        btn = self._first(SEL_SUBMIT)
        if not btn:
            return ""
        try:
            return (btn.inner_text() or "").strip()
        except Exception:
            return ""

    def _wait_idle(self, timeout: float = 90) -> None:
        """若上一次的连通性测试还在跑（按钮显示「测试中」且不可用），等它结束。"""
        deadline = time.time() + timeout
        while time.time() < deadline:
            btn = self._first(SEL_SUBMIT)
            if btn is None:
                return  # 弹窗已关（上一次已成功）
            text = self._submit_text()
            if not btn.is_disabled() or ("测试" not in text and "est" not in text):
                return
            time.sleep(0.6)

    def _reset_if_dirty(self) -> None:
        dirty = self.page.evaluate(
            """() => {
              for (const el of document.querySelectorAll('input,textarea')) {
                if (el.value) return true;
              }
              return false;
            }"""
        )
        if not dirty:
            return
        reset = self._first(SEL_RESET)
        if reset:
            reset.click(timeout=5000)
            time.sleep(0.8)

    def _has_save_directly(self) -> bool:
        """出现「直接保存」按钮 = 连通性测试失败（例如模型不支持图片输入）。"""
        return bool(
            self.page.evaluate(
                """() => {
                  for (const el of document.querySelectorAll('button,[role=button]')) {
                    const t = (el.textContent || '').trim();
                    if (t === '直接保存' || t === 'Save Directly') {
                      const r = el.getBoundingClientRect();
                      if (r.width > 10) return true;
                    }
                  }
                  return false;
                }"""
            )
        )

    def _click_save_directly(self) -> bool:
        return self._click_text(["直接保存", "Save Directly"])

    # -- 填表 + 提交 -------------------------------------------------------
    def add_model(self, model_id: str, display: str, url: str, api_key: str,
                  timeout: int = 300, on_test_fail: str = "save") -> str:
        """返回 added / saved-directly / exists / failed:<原因>。

        成功判定以「弹窗关闭」为准：提交后 Trae 会先做一次真实连通性测试
        （注意：测试请求带图片输入，纯文本模型必然失败），测试期间提交按钮显示
        「连通性测试中」并禁用。通过 → 弹窗关闭；失败 → 出现「直接保存」按钮，
        此时按 on_test_fail 决定是直接注册还是放弃。
        """
        self.open_custom_form()
        self._wait_idle()
        self._reset_if_dirty()

        url_input = self._first(SEL_URL)
        id_input = self._first(SEL_MODEL_ID)
        if not url_input or not id_input:
            raise RuntimeError("找不到地址 / 模型 ID 输入框")

        url_input.fill(url)
        id_input.fill(model_id)
        disp = self._first(SEL_DISPLAY)
        if disp:
            disp.fill(display)
        key = self._first(SEL_APIKEY)
        if key:
            key.fill(api_key)
        time.sleep(0.5)

        # 等提交按钮可用（表单校验通过）
        deadline = time.time() + 20
        submit = self._first(SEL_SUBMIT)
        while submit is not None and submit.is_disabled() and time.time() < deadline:
            time.sleep(0.4)
            submit = self._first(SEL_SUBMIT)
        if submit is None:
            raise RuntimeError("找不到「添加模型」提交按钮")
        if submit.is_disabled():
            raise RuntimeError("提交按钮不可用（表单校验未通过，检查模型 ID 是否重复或字段是否合法）")

        base_toast = self._read_toast()
        submit.click(timeout=8000)

        deadline = time.time() + timeout
        while time.time() < deadline:
            time.sleep(0.8)
            if not self.has_form():
                return "added"  # 弹窗关闭 = 测试通过并已保存
            if self._has_save_directly():
                # 连通性测试失败（常见：模型不支持图片输入 / 上游暂时不可用）
                if on_test_fail != "save":
                    return "failed:连通性测试未通过（可用 --on-test-fail save 直接注册）"
                if self._click_save_directly():
                    for _ in range(20):
                        time.sleep(0.5)
                        if not self.has_form():
                            return "saved-directly"
                    return "failed:点击「直接保存」后弹窗未关闭"
                return "failed:连通性测试失败且找不到「直接保存」"
            txt = self._read_toast()
            fresh = txt != base_toast
            low = txt.lower()
            if fresh and ("已存在" in txt or "already exists" in low):
                return "exists"
            if fresh and ("失败" in txt or "错误" in txt or "failed" in low):
                return "failed:" + txt.strip()[:160]
        return "failed:超时（连通性测试可能仍在进行，可用 --timeout 调大）"


def connect_page(cdp: str, timeout: int = 30):
    from playwright.sync_api import sync_playwright

    pw = sync_playwright().start()
    browser = pw.chromium.connect_over_cdp(cdp, timeout=timeout * 1000)
    pages = []
    for c in browser.contexts:
        pages.extend(c.pages)
    page = None
    for p in pages:
        try:
            title = p.title()
        except Exception:
            continue
        if "TraeWork" in title or "solo-lite" in p.url:
            page = p
            break
    if page is None and pages:
        page = pages[0]
    if page is None:
        raise RuntimeError("CDP 连上了，但没有找到 Trae 页面")
    page.bring_to_front()
    return pw, browser, page


# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------


def main() -> int:
    ap = argparse.ArgumentParser(
        description="把 LLM Switch 的模型同步进 Trae SOLO 的自定义模型列表"
    )
    ap.add_argument("--api", help=f"LLM Switch 的 OpenAI 兼容入口（默认 {DEFAULT_API}）")
    ap.add_argument("--url", help="要填进 Trae 的「自定义请求地址」（默认自动推导）")
    ap.add_argument("--api-key", default=DEFAULT_API_KEY, help="要填进 Trae 的 API Key（代理不校验）")
    ap.add_argument("--cdp", default=DEFAULT_CDP, help=f"CDP 地址（默认 {DEFAULT_CDP}）")
    ap.add_argument("--user-data-dir", help="Trae 用户数据目录（默认自动探测）")
    ap.add_argument("--display", default="{id}",
                    help="显示名模板，{id} 会替换成模型 ID（默认与模型 ID 相同）")
    ap.add_argument("--only", action="append", default=[],
                    help="只处理指定模型 ID（可重复；默认全部）")
    ap.add_argument("--dry-run", action="store_true", help="只对比，不修改 Trae")
    ap.add_argument("--configure-argv", action="store_true",
                    help="端口没开时自动写入 argv.json（需手动重启 Trae）")
    ap.add_argument("--restart", action="store_true",
                    help="端口没开时自动重启 Trae（会中断正在跑的会话）")
    ap.add_argument("--timeout", type=int, default=300,
                    help="单个模型的连通性测试等待上限（秒，默认 300）")
    ap.add_argument("--on-test-fail", choices=["save", "skip"], default="save",
                    help="连通性测试失败时：save=点「直接保存」仍然注册（默认），skip=跳过")
    ap.add_argument("--shots-dir", help="失败/过程截图目录（默认不截图）")
    args = ap.parse_args()

    api_base = resolve_api_base(args.api)
    target_url = args.url or api_base  # 不带 /chat/completions，Trae 会自动补

    log(f"LLM Switch 入口：{api_base}")
    try:
        models = fetch_models(api_base)
    except Exception as e:
        log(f"[错误] 读不到模型清单：{e}")
        log("       请确认 LLM Switch 正在运行、代理端口正确。")
        return 2
    if args.only:
        models = [m for m in models if m in set(args.only)]
    if not models:
        log("没有需要处理的模型（可检查 --only 或 LLM Switch 里的启用状态）。")
        return 0
    log(f"待处理模型 {len(models)} 个：{', '.join(models)}")

    user_data_dir = find_user_data_dir(args.user_data_dir)
    have: List[str] = []
    if user_data_dir:
        have = existing_custom_models(user_data_dir)
        log(f"Trae 已有自定义模型 {len(have)} 个（本地缓存）：{', '.join(have) if have else '无'}")
    else:
        log("[warn] 没找到 Trae 用户数据目录，跳过本地去重（会由 Trae 端做重复校验）")

    todo = [m for m in models if m not in have]
    if not todo:
        log("全部已存在，无需处理。")
        return 0
    log(f"需要新增 {len(todo)} 个：{', '.join(todo)}")

    if args.dry_run:
        log("--dry-run：未做任何修改。")
        return 0

    if not ensure_debug_port(args.cdp, args.configure_argv, args.restart):
        return 3

    pw = browser = None
    try:
        pw, browser, page = connect_page(args.cdp)
        ui = TraeUI(page, args.shots_dir)
        log(f"已连接 Trae 页面：{page.title()}")

        ok = skip = fail = direct = 0
        for i, model_id in enumerate(todo, 1):
            display = args.display.replace("{id}", model_id)
            log(f"[{i}/{len(todo)}] {model_id} …")
            try:
                result = ui.add_model(model_id, display, target_url, args.api_key,
                                      timeout=args.timeout, on_test_fail=args.on_test_fail)
            except Exception as e:
                result = f"failed:{e}"
                ui.shot(f"error-{model_id.replace('/', '_')}")
            if result == "added":
                ok += 1
                log("    ✓ 已添加（连通性测试通过）")
            elif result == "saved-directly":
                direct += 1
                log("    ✓ 已添加（连通性测试未通过——多为模型不支持图片输入，已直接保存）")
            elif result == "exists":
                skip += 1
                log("    - 已存在，跳过")
            else:
                fail += 1
                log(f"    ✗ {result}")
                ui.shot(f"error-{model_id.replace('/', '_')}")
            time.sleep(0.5)

        log("")
        log(f"完成：直接通过 {ok}，测试失败后直接保存 {direct}，跳过 {skip}，失败 {fail}")
        if fail:
            log("失败的模型可以重跑脚本重试；若提示连通性测试失败，先用 curl 直接调代理确认该模型可用：")
            log(f'  curl -s {api_base}/chat/completions -H "Content-Type: application/json" '
                f'-d "{{\\"model\\":\\"<模型ID>\\",\\"messages\\":[{{\\"role\\":\\"user\\",\\"content\\":\\"hi\\"}}]}}"')
        return 0 if fail == 0 else 1
    finally:
        try:
            if browser:
                browser.close()
            if pw:
                pw.stop()
        except Exception:
            pass


if __name__ == "__main__":
    sys.exit(main())
