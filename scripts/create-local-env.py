#!/usr/bin/env python3
"""Create development configuration without printing or overwriting secrets."""
import base64
import os
from pathlib import Path
import secrets

root = Path(__file__).resolve().parents[1]
path = root / '.env'
content = (root / '.env.example').read_text()
content = content.replace('POSTGRES_PASSWORD=\n', 'POSTGRES_PASSWORD=' + secrets.token_hex(24) + '\n')
content = content.replace('ENCRYPTION_KEY=\n', 'ENCRYPTION_KEY=' + base64.b64encode(secrets.token_bytes(32)).decode() + '\n')
try:
    fd = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    raise SystemExit('.env already exists; preserving it. Review it before starting.')
with os.fdopen(fd, 'w') as file:
    file.write(content)
print('Created .env with fresh local secrets and owner-only file permissions.')
