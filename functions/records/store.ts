import { randomUUID } from 'node:crypto';
import { Pool } from 'pg';
import { attachDatabasePool } from '@neon/functions';
import { S3Client, PutObjectCommand, GetObjectCommand, DeleteObjectCommand } from '@aws-sdk/client-s3';
import { migration } from './schema.js';
import { validatePerson, type Person, type PersonInput } from './model.js';
export interface Store {
 list(page:number,size:number):Promise<{items:Person[];total:number}>;
 get(id:string,dob?:string):Promise<Person|null>;
 create(data:PersonInput):Promise<Person>;
 update(id:string,data:Partial<PersonInput>):Promise<Person|null>;
 remove(id:string):Promise<boolean>;
 putPhoto(id:string,body:Uint8Array,type:string):Promise<boolean>;
 getPhoto(id:string):Promise<{body:Uint8Array;type:string}|null>;
 deletePhoto(id:string):Promise<boolean>;
 health():Promise<void>;
}
function person(row:any):Person {return {...row.data,id:row.id,unverified:true,has_photo:!!row.photo_key,created_at:row.created_at.toISOString(),updated_at:row.updated_at.toISOString()};}
export class NeonStore implements Store {
 private pool:Pool;
 private s3:S3Client;
 private ready?:Promise<void>;
 constructor(private bucket='assets') {
  if(!process.env.DATABASE_URL)throw new Error('DATABASE_URL required');
  this.pool=new Pool({connectionString:process.env.DATABASE_URL,max:3,connectionTimeoutMillis:10000,idleTimeoutMillis:20000,statement_timeout:10000});
  attachDatabasePool(this.pool);
  this.s3=new S3Client({forcePathStyle:true});
 }
 private async init() {
  if(!this.ready) this.ready=this.migrate().catch(e=>{this.ready=undefined;throw e;});
  await this.ready;
 }
 private async migrate() {
  const client=await this.pool.connect();
  try {await client.query('BEGIN');await client.query('SELECT pg_advisory_xact_lock(71398241)');await client.query(migration);await client.query('COMMIT');}
  catch(e){await client.query('ROLLBACK').catch(()=>{});throw e;}finally{client.release();}
 }
 async health(){await this.init();await this.pool.query('SELECT 1');}
 async list(page:number,size:number){await this.init();const rows=await this.pool.query('SELECT * FROM private_records.people ORDER BY created_at DESC,id LIMIT $1 OFFSET $2',[size,(page-1)*size]);const count=await this.pool.query('SELECT count(*)::int AS n FROM private_records.people');return {items:rows.rows.map(person),total:count.rows[0].n};}
 async get(id:string,dob?:string){await this.init();const result=await this.pool.query("SELECT * FROM private_records.people WHERE id=$1 AND ($2::text IS NULL OR data->>'date_of_birth'=$2)",[id,dob??null]);return result.rows[0]?person(result.rows[0]):null;}
 async create(data:PersonInput){await this.init();const result=await this.pool.query('INSERT INTO private_records.people(id,data) VALUES($1,$2) RETURNING *',['person_'+randomUUID(),JSON.stringify(data)]);return person(result.rows[0]);}
 async update(id:string,patch:Partial<PersonInput>){await this.init();const client=await this.pool.connect();try{await client.query('BEGIN');const old=await client.query('SELECT * FROM private_records.people WHERE id=$1 FOR UPDATE',[id]);if(!old.rows[0]){await client.query('ROLLBACK');return null;}const data=validatePerson({...old.rows[0].data,...patch});const result=await client.query('UPDATE private_records.people SET data=$2,updated_at=now() WHERE id=$1 RETURNING *',[id,JSON.stringify(data)]);await client.query('COMMIT');return person(result.rows[0]);}catch(e){await client.query('ROLLBACK').catch(()=>{});throw e;}finally{client.release();}}
 private async collect() {
  const rows=await this.pool.query('SELECT object_key FROM private_records.storage_cleanup ORDER BY created_at LIMIT 10');
  for(const row of rows.rows)try{await this.s3.send(new DeleteObjectCommand({Bucket:this.bucket,Key:row.object_key}),{abortSignal:AbortSignal.timeout(10000)});await this.pool.query('DELETE FROM private_records.storage_cleanup WHERE object_key=$1',[row.object_key]);}catch{console.warn('Private Records: storage cleanup pending');}
 }
 async remove(id:string){await this.init();const result=await this.pool.query(`WITH removed AS (DELETE FROM private_records.people WHERE id=$1 RETURNING photo_key), queued AS (INSERT INTO private_records.storage_cleanup(object_key) SELECT photo_key FROM removed WHERE photo_key IS NOT NULL ON CONFLICT DO NOTHING) SELECT count(*)::int AS n FROM removed`,[id]);await this.collect();return result.rows[0].n>0;}
 async putPhoto(id:string,body:Uint8Array,type:string){await this.init();if(!await this.get(id))return false;const key='people/'+randomUUID();await this.s3.send(new PutObjectCommand({Bucket:this.bucket,Key:key,Body:body,ContentType:type,CacheControl:'private, no-store'}),{abortSignal:AbortSignal.timeout(15000)});const client=await this.pool.connect();try{await client.query('BEGIN');const old=await client.query('SELECT photo_key FROM private_records.people WHERE id=$1 FOR UPDATE',[id]);if(!old.rows[0]){await client.query('INSERT INTO private_records.storage_cleanup(object_key) VALUES($1) ON CONFLICT DO NOTHING',[key]);await client.query('COMMIT');await this.collect();return false;}await client.query('UPDATE private_records.people SET photo_key=$2,photo_type=$3,updated_at=now() WHERE id=$1',[id,key,type]);if(old.rows[0].photo_key)await client.query('INSERT INTO private_records.storage_cleanup(object_key) VALUES($1) ON CONFLICT DO NOTHING',[old.rows[0].photo_key]);await client.query('COMMIT');}catch(e){await client.query('ROLLBACK').catch(()=>{});throw e;}finally{client.release();}await this.collect();return true;}
 async getPhoto(id:string){await this.init();const result=await this.pool.query('SELECT photo_key,photo_type FROM private_records.people WHERE id=$1',[id]);const row=result.rows[0];if(!row?.photo_key)return null;const object=await this.s3.send(new GetObjectCommand({Bucket:this.bucket,Key:row.photo_key}),{abortSignal:AbortSignal.timeout(15000)});return {body:await object.Body!.transformToByteArray(),type:row.photo_type};}
 async deletePhoto(id:string){await this.init();const result=await this.pool.query(`WITH old AS (SELECT id,photo_key FROM private_records.people WHERE id=$1 FOR UPDATE), changed AS (UPDATE private_records.people SET photo_key=NULL,photo_type=NULL,updated_at=now() WHERE id IN (SELECT id FROM old)), queued AS (INSERT INTO private_records.storage_cleanup(object_key) SELECT photo_key FROM old WHERE photo_key IS NOT NULL ON CONFLICT DO NOTHING) SELECT count(*)::int AS n FROM old`,[id]);await this.collect();return result.rows[0].n>0;}
}
