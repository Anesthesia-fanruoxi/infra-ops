import sqlite3, sys, json

DB = r'D:\project\go\infra-ops\data\infra-ops.db'
OUT = r'D:\project\go\infra-ops\script\_probe_out.txt'
RUN = 76
sys.stdout = open(OUT, 'w', encoding='utf-8', errors='replace')

conn = sqlite3.connect(DB)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

print('== params_json (pretty) ==')
p = cur.execute('SELECT params_json FROM stack_runs WHERE id=?', (RUN,)).fetchone()[0]
try:
    print(json.dumps(json.loads(p), ensure_ascii=False, indent=2, sort_keys=True))
except Exception as e:
    print('raw:', p)

print()
print('== all hosts: zookeeper-related log lines ==')
for r in cur.execute(
    "SELECT id, host_ip, phase, text FROM stack_run_logs WHERE run_id=?"
    " AND (text LIKE '%ZooKeeper%' OR text LIKE '%Mode%' OR text LIKE '%myid%' OR text LIKE '%zk%') ORDER BY id",
    (RUN,),
):
    print('[%s|%s|%s] %s' % (r['id'], r['host_ip'], r['phase'], r['text']))

conn.close()
print()
print('DONE')
