import hashlib
from pathlib import Path

def source_fingerprint():
    repo=Path(__file__).resolve().parents[2]
    files=set()
    for rel in ('server/internal/cn35game','server/internal/cn35store','server/internal/database','server/internal/cn35app','server/internal/config','server/assets/cn350'):
        files.update(p for p in (repo/rel).rglob('*') if p.is_file() and p.suffix in ('.go','.sql','.json'))
    h=hashlib.sha256()
    for p in sorted(files):
        h.update(str(p.relative_to(repo)).encode());h.update(b'\0');h.update(hashlib.sha256(p.read_bytes()).digest())
    return h.hexdigest()
