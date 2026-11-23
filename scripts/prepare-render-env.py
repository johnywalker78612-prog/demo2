#!/usr/bin/env python3
"""Export only Render runtime settings, without printing credentials."""
import json
import os
from pathlib import Path
import shlex

ROOT = Path(__file__).resolve().parent.parent

def read_env(path):
    values = {}
    if not path.exists():
        return values
    for line in path.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith('#') or '=' not in line:
            continue
        key, value = line.split('=', 1)
        parts = shlex.split(value, comments=True)
        values[key.removeprefix('export ').strip()] = ' '.join(parts)
    return values

def main():
    neon = read_env(ROOT / '.env.neon.local')
    local = read_env(ROOT / 'backend/.env')
    values = {
        'RECORDS_CLOUD': 'true',
        'RECORDS_SECURE_COOKIES': 'true',
        'DATABASE_URL': neon.get('DATABASE_URL', ''),
    }
    for key in ('RECORDS_DATA_KEY', 'RECORDS_ADMIN_PASSWORD', 'RECORDS_API_TOKEN',
                'RECORDS_ADMIN_USERNAME', 'RECORDS_ADMIN_EMAIL'):
        if local.get(key):
            values[key] = local[key]
    values['FROM_EMAIL'] = 'noreply@evisa-immigration.uk'
    values['FROM_NAME'] = 'Private Records'
    # The Render URL is inferred from RENDER_EXTERNAL_URL for the first deploy.
    # Set RECORDS_PUBLIC_URL in Render only after the custom domain works.
    required = ('DATABASE_URL', 'RECORDS_DATA_KEY', 'RECORDS_ADMIN_PASSWORD', 'RECORDS_API_TOKEN')
    missing = [key for key in required if not values.get(key)]
    if missing:
        raise SystemExit('Missing local configuration: ' + ', '.join(missing))
    target = ROOT / '.env.render.local'
    if target.exists():
        raise SystemExit('.env.render.local already exists; edit it directly to preserve changes.')
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as output:
        for key, value in values.items():
            output.write(key + '=' + json.dumps(value) + '\n')
    print('Created .env.render.local (mode 0600). Existing environment files unchanged.')
    print('Configure Brevo or SMTP separately to enable email delivery.')

if __name__ == '__main__':
    main()
