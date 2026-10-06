#!/usr/bin/env python3
"""Download unmodified official UI for local fixture testing only; no credentials."""
from pathlib import Path
import urllib.request,hashlib
url='https://github.com/router-for-me/Cli-Proxy-API-Management-Center/releases/download/v1.25.3/management.html'
sha='866bae020785b59126a209e89389b77f673fd791beb2208ea650dcc07b3a108d'
b=urllib.request.urlopen(url).read()
assert hashlib.sha256(b).hexdigest()==sha,'Official release hash mismatch'
p=Path.home()/'.cache/cpamc-reference/management.html';p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(b)
print(p)
