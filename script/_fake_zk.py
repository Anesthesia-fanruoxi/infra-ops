"""一次性假 ZK：5 个端点 12181-12185，前 4 个 follower、第 5 个 leader（对应 run 76 拓扑）。"""
import socket
import threading
import time


def serve(port: int, state: str) -> None:
    s = socket.socket()
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(('127.0.0.1', port))
    s.listen(16)
    while True:
        c, _ = s.accept()
        try:
            c.settimeout(3)
            try:
                c.recv(64)  # 读掉 mntr
            except Exception:
                pass
            c.sendall(('zk_version 3.9.2\nzk_server_state\t%s\nzk_peer_count 5\n' % state).encode())
        finally:
            c.close()


for i in range(5):
    threading.Thread(target=serve, args=(12181 + i, 'leader' if i == 4 else 'follower'), daemon=True).start()
time.sleep(300)
