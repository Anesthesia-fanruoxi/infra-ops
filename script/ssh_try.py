# -*- coding: utf-8 -*-
"""SSH 连接测试脚本：python ssh_try.py host port user password"""
import sys
import paramiko


def main():
    if len(sys.argv) < 5:
        print("用法: python ssh_try.py <host> <port> <user> <password>")
        sys.exit(1)
    host, port, user, password = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]

    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    try:
        client.connect(host, port=port, username=user, password=password, timeout=8)
        stdin, stdout, stderr = client.exec_command("ls")
        print("OK: connection established")
        print("ls 输出:", stdout.read().decode("utf-8", "ignore").strip())
    except Exception as e:
        print("FAIL:", e)
        sys.exit(1)
    finally:
        client.close()


if __name__ == "__main__":
    main()
