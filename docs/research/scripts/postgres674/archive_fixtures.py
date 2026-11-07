"""Archive owned test fixtures with verified content and link metadata."""
import hashlib
import json
import os
from pathlib import Path
import stat
import sys
import tarfile
import uuid
from outputs import new_output_directory, write_new_text


def archive(root, parent):
    root=Path(root)
    with new_output_directory(Path(parent)/('fixtures-'+uuid.uuid4().hex)) as out:
        records={}
        for p in [root,*sorted(root.rglob('*'))]:
            s=p.lstat();name=str(p.relative_to(root.parent))
            item=dict(mode=stat.S_IMODE(s.st_mode),nlink=s.st_nlink,device=s.st_dev,inode=s.st_ino,size=s.st_size)
            if p.is_symlink():item['symlink']=os.readlink(p)
            elif p.is_file():item['sha256']=hashlib.sha256(p.read_bytes()).hexdigest()
            records[name]=item
        with (out/'fixtures.tar.gz').open('xb') as file:
            with tarfile.open(fileobj=file,mode='w:gz',dereference=False) as tar:tar.add(root,arcname=root.name)
        (out/'fixtures.tar.gz').chmod(0o600)
        with tarfile.open(out/'fixtures.tar.gz') as tar:
            assert set(tar.getnames())==set(records)
            for member in tar:
                item=records[member.name]
                assert member.mode==item['mode']
                if member.issym():assert member.linkname==item['symlink']
                if member.isfile() or member.islnk():
                    with tar.extractfile(member) as file:assert hashlib.sha256(file.read()).hexdigest()==item['sha256']
                item.update(archive_type=member.type.decode(),archive_link=member.linkname)
                if member.islnk():assert records[member.linkname]['inode']==item['inode'] and records[member.linkname]['device']==item['device']
        write_new_text(out/'manifest.json',json.dumps(dict(members=records,archive_sha256=hashlib.sha256((out/'fixtures.tar.gz').read_bytes()).hexdigest(),verified=True),indent=2)+'\n')


if __name__=='__main__':archive(*sys.argv[1:])
