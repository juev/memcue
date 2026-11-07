"""Read-only name-discovery fallback, scoped to the ongoing matched study.

No environment, compose, secrets, or unrelated container discovery. This
supplement starts after the main collector's exact-project-label miss.
"""
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

from matched_query import safe_read
from matched_study import Stats, OWN, OUT, save


def main():
    os.umask(0o077)
    discovery=json.loads(safe_read(OWN,'stats-name-discovery.json',{'stats-name-discovery.json'}))
    rows=[line.split() for line in discovery['stdout'].splitlines()]
    if discovery['exit'] or len(rows)!=1 or len(rows[0])!=2:
        raise ValueError('exact named research container unavailable')
    ident,name=rows[0]
    if not re.fullmatch('[a-f0-9]{12,64}',ident) or 'memcue-research' not in name:
        raise ValueError('research identity rejected')
    dest=OWN/'stats-supplement';dest.mkdir(mode=0o700)
    class NamedStats(Stats):
        def start(self):
            # Reuse collector parsing and guaranteed stop/reap with fixed discovery.
            self.argv=['ssh','-oBatchMode=yes','-oConnectTimeout=5','loffe',"timeout 600s docker stats --format '{{json .}}' "+ident]
            self.err=(dest/'stats-stderr.txt').open('x')
            self.process=subprocess.Popen(self.argv,stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=self.err,text=True,start_new_session=True)
            import threading
            def collect():
                for line in self.process.stdout:
                    try:
                        value=json.loads(line[line.find('{'):]);value.update(phase=self.phase,monotonic_s=time.monotonic())
                        self.rows.append(value);self.raw.write(value)
                    except ValueError:pass
            self.thread=threading.Thread(target=collect,daemon=True);self.thread.start();self.status='sampling'
    stats=NamedStats(dest);stats.phase='remaining-matched-study'
    start=time.monotonic();stats.start()
    try:
        while time.monotonic()-start<600:
            if (OUT/'complete.json').exists() or (OUT/'failure-run.txt').exists():break
            time.sleep(1)
    finally:
        stats.stop()
        save(dest/'summary.json',dict(**stats.summary,wall_s=time.monotonic()-start,argv=sys.orig_argv,
                                     scope='Only remainder of active study after project-label discovery miss; excludes earlier work. Co-resident activity not isolated.'))
    return 0

if __name__=='__main__':sys.exit(main())
