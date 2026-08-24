# -*- coding: utf-8 -*-
"""
client_simulator.py
作者: reformLi
创建日期: 2026/8/24
最后修改: 2026/8/24
版本: 1.1.0

功能描述: 模拟客户端压测 proxy-sentinel 代理网关（可运行版）
"""

#!/usr/bin/env python3
"""
模拟客户端调用 proxy-sentinel 代理网关。

功能：
- 支持 GET、POST、PUT、DELETE 等方法
- 支持随机选择路径（/get, /post, /status/200, /status/404, /delay/1, /headers, /ip）
- 支持并发和循环请求
- 统计成功/失败/状态码分布
- 自动携带 Authorization: Bearer <token>
- 支持自定义代理地址、请求总数、并发数等

用法：
    python client_simulator.py --url http://localhost:8080 --token dev-token-123 --count 100 --concurrency 10

注意：
    网关代理路由挂在 /proxy/* 前缀下（v1.0 脚本因缺前缀全部 404 且不落库）。
    本脚本自动拼接前缀：--url 传网关根地址即可，带不带 /proxy 都行。
"""

import argparse
import random
import time
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from collections import Counter

import requests

# ---------- 配置 ----------
DEFAULT_PROXY_URL = "http://localhost:8080"
DEFAULT_TOKEN = "dev-token-123"
DEFAULT_COUNT = 50
DEFAULT_CONCURRENCY = 5
PROXY_PREFIX = "/proxy"

# 可用的后端路径（随机选择；转发时自动加 /proxy 前缀）
PATHS = [
    "/get",                # GET 请求，回显参数
    "/post",               # POST 请求
    "/put",                # PUT 请求
    "/delete",             # DELETE 请求
    "/status/200",         # 返回 200 OK
    "/status/404",         # 返回 404
    "/status/500",         # 返回 500
    "/delay/1",            # 延迟 1 秒
    "/headers",            # 返回请求头
    "/ip",                 # 返回客户端 IP
    "/anything",           # 回显所有信息
]

# 状态码分类
STATUS_CATEGORIES = {
    range(200, 300): "2xx",
    range(300, 400): "3xx",
    range(400, 500): "4xx",
    range(500, 600): "5xx",
}


def categorize_status(status):
    for rng, cat in STATUS_CATEGORIES.items():
        if status in rng:
            return cat
    return "unknown"


def build_proxy_url(base):
    """规范化网关地址：去尾斜杠，确保以 /proxy 结尾。

    兼容三种写法：http://host:8080、http://host:8080/、http://host:8080/proxy
    """
    u = base.rstrip("/")
    if u.endswith(PROXY_PREFIX):
        return u
    return u + PROXY_PREFIX


def send_request(proxy_url, token, method, path, data=None):
    """发送单次请求并返回结果字典。"""
    url = proxy_url + path
    headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json",
    }
    start = time.time()
    try:
        if method == "GET":
            resp = requests.get(url, headers=headers, timeout=10)
        elif method == "POST":
            resp = requests.post(url, headers=headers, json=data or {"test": "value"}, timeout=10)
        elif method == "PUT":
            resp = requests.put(url, headers=headers, json=data or {"test": "value"}, timeout=10)
        elif method == "DELETE":
            resp = requests.delete(url, headers=headers, timeout=10)
        else:
            resp = requests.get(url, headers=headers, timeout=10)  # fallback

        elapsed = time.time() - start
        return {
            "success": resp.status_code < 400,  # 4xx/5xx 都算失败（网关拒绝或后端错误）
            "status": resp.status_code,
            "method": method,
            "path": path,
            "duration": elapsed,
            "error": None,
        }
    except Exception as e:
        elapsed = time.time() - start
        return {
            "success": False,
            "status": None,
            "method": method,
            "path": path,
            "duration": elapsed,
            "error": str(e),
        }


def worker(args):
    """线程工作函数。"""
    return send_request(*args)


def preflight(proxy_url, token):
    """预检：确认网关可达且 Token 有效，避免白跑一轮。"""
    url = proxy_url + "/get"
    try:
        resp = requests.get(url, headers={"Authorization": f"Bearer {token}"}, timeout=5)
        if resp.status_code == 200:
            return None
        if resp.status_code == 401:
            return "Token 无效或未携带（401）：请求会被网关拒绝且不落库，请检查 --token"
        return f"预检返回 {resp.status_code}：{resp.text[:200]}"
    except Exception as e:
        return f"网关不可达（{proxy_url}）：{e}\n  先启动网关：go run ./cmd/sentinel"


def main():
    parser = argparse.ArgumentParser(description="模拟客户端测试代理网关")
    parser.add_argument("--url", default=DEFAULT_PROXY_URL, help=f"网关地址 (默认 {DEFAULT_PROXY_URL})")
    parser.add_argument("--token", default=DEFAULT_TOKEN, help=f"Bearer Token (默认 {DEFAULT_TOKEN})")
    parser.add_argument("--count", type=int, default=DEFAULT_COUNT, help=f"总请求数 (默认 {DEFAULT_COUNT})")
    parser.add_argument("--concurrency", type=int, default=DEFAULT_CONCURRENCY, help=f"并发数 (默认 {DEFAULT_CONCURRENCY})")
    parser.add_argument("--methods", nargs="+", default=["GET", "POST", "PUT", "DELETE"],
                        help="允许的 HTTP 方法列表 (默认 GET POST PUT DELETE)")
    parser.add_argument("--seed", type=int, default=None, help="随机种子，用于复现")
    args = parser.parse_args()

    if args.seed is not None:
        random.seed(args.seed)

    proxy_url = build_proxy_url(args.url)

    # 预检：网关是否可达、Token 是否有效
    problem = preflight(proxy_url, args.token)
    if problem:
        print(f"❌ 预检失败：{problem}", file=sys.stderr)
        sys.exit(1)

    # 准备请求任务列表
    tasks = []
    for i in range(args.count):
        method = random.choice(args.methods)
        path = random.choice(PATHS)
        # POST/PUT 可能带 data
        data = {"id": i, "timestamp": time.time()} if method in ("POST", "PUT") else None
        tasks.append((proxy_url, args.token, method, path, data))

    print(f"🚀 开始发送 {args.count} 个请求 (并发 {args.concurrency})...")
    print(f"📍 网关地址: {proxy_url}  (代理前缀 /proxy)")
    print(f"🔑 Token: {args.token}")
    print(f"📋 方法: {', '.join(args.methods)}\n")

    # 使用线程池并发执行
    results = []
    with ThreadPoolExecutor(max_workers=args.concurrency) as executor:
        future_to_task = {executor.submit(worker, task): task for task in tasks}
        for future in as_completed(future_to_task):
            result = future.result()
            results.append(result)
            # 实时打印进度
            sys.stdout.write(".")
            sys.stdout.flush()
    print("\n")

    # 统计
    total = len(results)
    success = sum(1 for r in results if r["success"])
    failed = total - success
    status_counter = Counter(r["status"] for r in results if r["status"] is not None)
    method_counter = Counter(r["method"] for r in results)
    path_counter = Counter(r["path"] for r in results)
    duration_list = [r["duration"] for r in results if r["duration"] is not None]
    avg_duration = sum(duration_list) / len(duration_list) if duration_list else 0

    # 分类统计
    status_category_counter = Counter()
    for r in results:
        if r["status"] is not None:
            status_category_counter[categorize_status(r["status"])] += 1

    print("📊 统计结果:")
    print(f"  总请求数: {total}")
    print(f"  成功: {success} ({success/total*100:.1f}%)")
    print(f"  失败: {failed} ({failed/total*100:.1f}%)")
    print(f"  平均耗时: {avg_duration:.3f} 秒")
    if duration_list:
        print(f"  最大耗时: {max(duration_list):.3f} 秒")
        print(f"  最小耗时: {min(duration_list):.3f} 秒")
    print("\n状态码分布:")
    for status, count in status_counter.most_common():
        print(f"    {status}: {count} ({count/total*100:.1f}%)")
    print("\n状态码类别分布:")
    for cat, count in status_category_counter.most_common():
        print(f"    {cat}: {count} ({count/total*100:.1f}%)")
    print("\n方法分布:")
    for method, count in method_counter.most_common():
        print(f"    {method}: {count} ({count/total*100:.1f}%)")
    print("\n路径分布 (Top 10):")
    for path, count in path_counter.most_common(10):
        print(f"    {path}: {count} ({count/total*100:.1f}%)")
    print(f"\n💡 以上请求已进入网关代理链路，可在前端「日志查询」页查看（含链路 ID）")

    # 输出错误详情（如果有）
    errors = [r for r in results if r["error"]]
    if errors:
        print(f"\n⚠️ 错误详情 (共 {len(errors)} 个):")
        for i, err in enumerate(errors[:5], 1):
            print(f"  {i}. [{err['method']} {err['path']}] {err['error']}")
        if len(errors) > 5:
            print(f"  ... 还有 {len(errors)-5} 个错误")


if __name__ == "__main__":
    main()
