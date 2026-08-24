# -*- coding: utf-8 -*-
"""
proxy_backend.py
作者: reformLi
创建日期: 2026/8/24
最后修改: 2026/8/24
版本: 1.0.0

功能描述: 用于测试代理网关的 Python 后端服务。它支持自动端口递增，并提供了与 httpbin 类似的接口，方便你验证负载均衡、限流、灰度等功能。
"""
"""
Mock 后端服务 for Proxy Sentinel 测试
- 支持自动端口递增（如果指定端口被占用则自动 +1）
- 支持 /get, /post, /put, /delete, /status/<code>, /delay/<seconds>, /headers, /ip, /anything
- 支持按百分比随机返回 HTTP 错误 (404, 403, 500, 502, 503, 429)
- 返回 JSON 格式的请求回显信息

打开多个终端窗口（推荐，最简单）

启动第一个实例：
python proxy_backend.py --port 18080
点击终端左上角的 + 号，新建一个终端标签页（或按 Ctrl+Shift+T）。

在第二个终端中启动第二个实例：
python proxy_backend.py --port 18081

再新建一个终端，启动第三个实例：
python proxy_backend.py --port 18082

"""
#!/usr/bin/env python3

import socket
import json
import time
import argparse
import random
from flask import Flask, request, jsonify

app = Flask(__name__)

# 全局错误率配置 (0~100)
ERROR_RATE = 0
# 错误状态码列表（可自定义）
ERROR_CODES = [400, 401, 403, 404, 429, 500, 502, 503, 504]


# ---------- 根路径 / 和 /anything 路由 ----------
@app.route('/', defaults={'path': ''}, methods=['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS'])
@app.route('/<path:path>', methods=['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS'])
def catch_all(path):
    """统一处理所有请求，回显请求信息"""
    # 1. 特殊路径优先处理（/status/<code> 或 /delay/<seconds>）
    # 这些路径不受到错误率影响，保证功能可测
    if path.startswith('status/'):
        try:
            status_code = int(path.split('/')[1])
            return jsonify({"message": f"Manual status {status_code}"}), status_code
        except ValueError:
            pass

    if path.startswith('delay/'):
        try:
            delay = float(path.split('/')[1])
            time.sleep(delay)
        except ValueError:
            pass

    # 2. 错误模拟（如果启用了错误率）
    global ERROR_RATE
    if ERROR_RATE > 0 and random.randint(0, 99) < ERROR_RATE:
        error_code = random.choice(ERROR_CODES)
        error_msg = {
            "error": f"Simulated error (rate {ERROR_RATE}%)",
            "code": error_code,
            "path": '/' + path if path else '/',
            "method": request.method,
            "timestamp": time.time()
        }
        return jsonify(error_msg), error_code

    # 3. 正常响应：回显请求信息
    method = request.method
    headers = dict(request.headers)
    client_ip = request.remote_addr
    args = request.args.to_dict()

    body = None
    if request.data:
        try:
            body = request.get_json(force=True, silent=True)
            if body is None:
                body = request.data.decode('utf-8')
        except Exception:
            body = request.data.decode('utf-8')

    response_data = {
        "method": method,
        "path": '/' + path if path else '/',
        "query": args,
        "headers": headers,
        "client_ip": client_ip,
        "body": body,
        "timestamp": time.time(),
        "server_port": request.environ.get('SERVER_PORT')
    }
    return jsonify(response_data), 200


# ---------- 健康检查端点 ----------
@app.route('/health', methods=['GET'])
def health():
    return jsonify({"status": "ok", "port": request.environ.get('SERVER_PORT')}), 200


# ---------- 端口查找逻辑 ----------
def find_free_port(start_port):
    """从 start_port 开始尝试，返回第一个可用端口"""
    port = start_port
    while True:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            try:
                s.bind(('0.0.0.0', port))
                return port
            except OSError:
                port += 1


# ---------- 主入口 ----------
if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='Mock 后端服务')
    parser.add_argument('--port', type=int, default=18080, help='起始端口 (默认 18080)')
    parser.add_argument('--error-rate', type=int, default=0, choices=range(0, 101),
                        help='错误率 (0-100)，默认 0 表示无错误')
    args = parser.parse_args()

    port = find_free_port(args.port)
    ERROR_RATE = args.error_rate

    print("✅ 后端服务启动，监听端口: " + str(port))
    print("   - 错误率: {}%".format(ERROR_RATE))
    print("   - 测试 GET:  curl http://localhost:{}/get".format(port))
    # 内部花括号转义为 {{ 和 }}
    print('   - 测试 POST: curl -X POST http://localhost:{}/post -H "Content-Type: application/json" -d \'{{"key":"value"}}\''.format(port))
    print("   - 测试状态码: curl http://localhost:{}/status/404".format(port))
    print("   - 测试延迟:   curl http://localhost:{}/delay/2".format(port))
    print("   - 健康检查:   curl http://localhost:{}/health".format(port))
    print("   - 回显所有:   curl http://localhost:{}/anything".format(port))
    print("\n按 Ctrl+C 停止服务\n")

    app.run(host='0.0.0.0', port=port, debug=False, threaded=True)