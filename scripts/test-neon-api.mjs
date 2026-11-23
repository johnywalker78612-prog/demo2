import assert from 'node:assert/strict';
const root=process.env.RECORDS_API_URL;
const token=process.env.RECORDS_API_TOKEN;
if(!root||!token)throw new Error('Set RECORDS_API_URL and RECORDS_API_TOKEN');
async function request(path,{method='GET',body,auth=true,type='application/json',status=200}={}) {
 const headers={};if(auth)headers.Authorization='Bearer '+token;if(body!==undefined)headers['Content-Type']=type;
 const r=await fetch(new URL(path,root),{method,headers,body:body===undefined?undefined:type==='application/json'?JSON.stringify(body):body,signal:AbortSignal.timeout(45000),redirect:'error'});
 assert.equal(r.status,status,`${method} ${path} returned ${r.status}, expected ${status}`);return r;
}
let id;
try {
 await request('/healthz',{auth:false});console.log('PASS deployed health');
 await request('/api/v1/people',{auth:false,status:401});console.log('PASS unauthorized access rejected');
 await request('/api/v1/health');console.log('PASS Postgres schema and connection');
 const p=await(await request('/api/v1/people',{method:'POST',status:201,body:{first_name:'Synthetic',last_name:'Integration Check',email:'integration@example.invalid',date_of_birth:'1990-03-09',passport_number:'TEST-ONLY',recorded_status:'Synthetic test record'}})).json();id=p.id;
 assert.equal(p.unverified,true);console.log('PASS create record');
 assert.equal((await(await request('/api/v1/people/'+id)).json()).id,id);console.log('PASS read record');
 await request('/api/v1/people/lookup',{method:'POST',body:{id,date_of_birth:'1990-03-09'}});
 await request('/api/v1/people/lookup',{method:'POST',body:{id,date_of_birth:'1991-03-09'},status:404});console.log('PASS matching and mismatched DOB lookup');
 const updated=await(await request('/api/v1/people/'+id,{method:'PATCH',body:{nationality:'TEST'}})).json();assert.equal(updated.nationality,'TEST');console.log('PASS update record');
 const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aE1sAAAAASUVORK5CYII=','base64');
 await request('/api/v1/people/'+id+'/photo',{method:'PUT',body:png,type:'image/png'});
 let photo=await request('/api/v1/people/'+id+'/photo');assert.deepEqual(Buffer.from(await photo.arrayBuffer()),png);assert.equal((await(await request('/api/v1/people/'+id)).json()).has_photo,true);console.log('PASS private photo upload/download');
 await request('/api/v1/people/'+id+'/photo',{method:'DELETE',status:204});await request('/api/v1/people/'+id+'/photo',{status:404});console.log('PASS delete photo');
 await request('/api/v1/people/'+id+'/photo',{method:'PUT',body:png,type:'image/png'});
 await request('/api/v1/people/'+id,{method:'DELETE',status:204});await request('/api/v1/people/'+id,{status:404});await request('/api/v1/people/'+id+'/photo',{status:404});id=undefined;console.log('PASS delete record and associated photo');
 console.log('All live API checks passed; synthetic record removed.');
} finally {
 if(id)await request('/api/v1/people/'+id,{method:'DELETE',status:204}).catch(()=>console.error('Cleanup failed; remove the synthetic integration-test record.'));
}
