// HTTPS fallback for networks that block Postgres TCP. No credentials are logged.
// Usage: node scripts/migrate-cloud-store.mjs [env-file] [encrypted-file]
import { neon } from '@neondatabase/serverless';
import { readFileSync } from 'node:fs';
import { parseEnv } from 'node:util';
import { createDecipheriv } from 'node:crypto';

try {
  const settings = parseEnv(readFileSync(process.argv[2] || '.env.neon.local', 'utf8'));
  const connection = settings.DATABASE_URL_UNPOOLED || settings.RECORDS_TEST_DATABASE_URL;
  if (!connection) throw new Error('Direct database URL is missing');
  const sql = neon(connection);
  const migration = readFileSync('backend/internal/app/migrations/001_cloud_store.sql', 'utf8');
  await sql.query(migration, []);
  console.log('Cloud store schema is ready.');

  if (process.argv[3]) {
    const local = parseEnv(readFileSync('backend/.env', 'utf8'));
    const key = Buffer.from(local.RECORDS_DATA_KEY || '', 'base64');
    const raw = readFileSync(process.argv[3]);
    if (key.length !== 32 || raw.length < 28 || raw.length > (32 << 20) + 28) {
      throw new Error('Invalid key or snapshot size');
    }
    const decipher = createDecipheriv('aes-256-gcm', key, raw.subarray(0, 12));
    decipher.setAuthTag(raw.subarray(-16));
    decipher.setAAD(Buffer.from('private-records-v1'));
    const plain = Buffer.concat([decipher.update(raw.subarray(12, -16)), decipher.final()]);
    const data = JSON.parse(plain.toString('utf8'));
    if (!data.people || !data.photos || !data.outbox) throw new Error('Unrecognized record file');
    // Import only when no cloud snapshot exists. Unlike the Go import helper,
    // this conservative fallback refuses even an already initialized empty row.
    const result = await sql.query(
      'INSERT INTO private_records_cloud_state (id, payload) VALUES (1, decode($1, \'hex\')) ON CONFLICT (id) DO NOTHING RETURNING id',
      [raw.toString('hex')],
    );
    if (result.length !== 1) throw new Error('Cloud snapshot already exists; refusing to overwrite');
    const rows = await sql`SELECT encode(payload, 'hex') AS encrypted FROM private_records_cloud_state WHERE id = 1`;
    if (rows[0]?.encrypted !== raw.toString('hex')) throw new Error('Imported snapshot verification failed');
    console.log('Encrypted records imported and verified byte-for-byte. Local file unchanged.');
  }
} catch {
  console.error('Cloud setup failed. Check connectivity, credentials, encryption key and whether a cloud snapshot already exists. No existing snapshot was overwritten.');
  process.exitCode = 1;
}
