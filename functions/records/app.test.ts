import { test } from 'node:test';
import assert from 'node:assert/strict';
import {createApp} from './app.js';
import {validatePerson} from './model.js';
import type {Store} from './store.js';
const token='test-only-admin-secret-at-least-32-characters';
const headers={Authorization:'Bearer '+token,'Content-Type':'application/json'};
test('unauthenticated requests cannot reach storage, including lookup and photos',async()=>{
 const store=new Proxy({},{get(){throw new Error('Storage accessed without authentication');}}) as Store;
 const app=createApp(store,token);
 for(const [path,method] of [['/api/v1/people','POST'],['/api/v1/people/lookup','POST'],['/api/v1/people/example','DELETE'],['/api/v1/people/example/photo','GET']]){
  const r=await app.request(path,{method});assert.equal(r.status,401);
 }
});
test('lookup requires BOTH internal ID and date of birth',async()=>{
 const store={get:async(id:string,dob?:string)=>id==='person_example'&&dob==='1990-03-09'?{id}:null} as unknown as Store;
 const app=createApp(store,token);
 for(const [body,status] of [[{id:'person_example',date_of_birth:'1990-03-09'},200],[{id:'person_example',date_of_birth:'1991-03-09'},404],[{id:'missing',date_of_birth:'1990-03-09'},404],[{id:'person_example'},422],[{date_of_birth:'1990-03-09'},422],[{id:'x',date_of_birth:'2025-02-30'},422],[{id:'x',date_of_birth:''},422]] as const){
  const r=await app.request('/api/v1/people/lookup',{method:'POST',headers,body:JSON.stringify(body)});assert.equal(r.status,status);
 }
});
test('validation prevents overwriting server fields and invalid dates',()=>{
 for(const body of [{first_name:'Example',id:'owned'},{first_name:'Example',unverified:false},{first_name:'Example',date_of_birth:'2025-02-30'},{first_name:'Example',valid_from:'2026-05-01',valid_until:'2026-04-01'}])assert.throws(()=>validatePerson(body));
 assert.equal(validatePerson({first_name:' Example '}).first_name,'Example');
});
test('malformed JSON and non-image uploads are rejected',async()=>{
 const app=createApp({} as Store,token);
 assert.equal((await app.request('/api/v1/people',{method:'POST',headers,body:'{'})).status,400);
 assert.equal((await app.request('/api/v1/people/x/photo',{method:'PUT',headers:{Authorization:'Bearer '+token,'Content-Type':'image/png'},body:'not a photo'})).status,422);
});
