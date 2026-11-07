"""Extract pinned Debian packages into an owned cache; no root or service setup."""
import argparse,hashlib,json,subprocess,urllib.request
from pathlib import Path
HERE=Path(__file__).resolve().parent
if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('cache');a=parser.parse_args();cache=Path(a.cache).resolve();cache.mkdir(mode=0o700,parents=True,exist_ok=True)
    root=cache/'pgroot';root.mkdir(exist_ok=True)
    for package in json.loads((HERE/'packages.json').read_text()):
        f=cache/Path(package['Filename']).name
        if not f.exists():
            with urllib.request.urlopen('https://deb.debian.org/debian/'+package['Filename'],timeout=120) as r, f.open('wb') as out:
                while chunk:=r.read(1024**2): out.write(chunk)
        assert hashlib.sha256(f.read_bytes()).hexdigest()==package['SHA256'],package['Package']
        subprocess.run(['dpkg-deb','-x',str(f),str(root)],check=True)
        print(package['Package'],package['Version'])
    print('Extracted cache only. Requires compatible Debian 13 runtime libraries; JIT stays disabled.')
