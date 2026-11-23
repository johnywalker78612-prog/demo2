import { Hono } from 'hono';
import { bodyLimit } from 'hono/body-limit';
import { timingSafeEqual } from 'node:crypto';
import { ZodError } from 'zod';
import { validatePerson, patchFields, lookupFields } from './model.js';
import type { Store } from './store.js';
const notFound={error:'Record not found'};
export function createApp(store:Store,token:string) {
 if(token.length<32)throw new Error('RECORDS_API_TOKEN must contain at least 32 characters');
 const expected=Buffer.from('Bearer '+token);
 const app=new Hono();
 app.use('*',async(c,next)=>{c.header('Cache-Control','no-store');c.header('X-Content-Type-Options','nosniff');await next();});
 app.get('/healthz',c=>c.json({status:'ok',service:'Private Records API'}));
 app.use('/api/*',async(c,next)=>{const actual=Buffer.from(c.req.header('Authorization')??'');if(actual.length!==expected.length || !timingSafeEqual(actual,expected))return c.json({error:'Unauthorized'},401);await next();});
 app.use('/api/*',bodyLimit({maxSize:5*1024*1024,onError:c=>c.json({error:'Request too large'},413)}));
 async function json(c:any){if(!(c.req.header('Content-Type')??'').toLowerCase().startsWith('application/json'))throw new Error('content_type');return c.req.json();}
 app.get('/api/v1/health',async c=>{await store.health();return c.json({database:'ok'});});
 app.get('/api/v1/people',async c=>{const page=Number(c.req.query('page')??1),size=Number(c.req.query('page_size')??25);if(!Number.isSafeInteger(page)||page<1||page>1000000||!Number.isSafeInteger(size)||size<1||size>100)return c.json({error:'Invalid pagination'},422);return c.json({...await store.list(page,size),page,page_size:size});});
 app.post('/api/v1/people',async c=>{const p=await store.create(validatePerson(await json(c)));c.header('Location','/api/v1/people/'+p.id);return c.json(p,201);});
 app.post('/api/v1/people/lookup',async c=>{const input=lookupFields.parse(await json(c));const p=await store.get(input.id,input.date_of_birth);return p?c.json(p):c.json(notFound,404);});
 app.get('/api/v1/people/:id',async c=>{const p=await store.get(c.req.param('id'));return p?c.json(p):c.json(notFound,404);});
 app.patch('/api/v1/people/:id',async c=>{const p=await store.update(c.req.param('id'),patchFields.parse(await json(c)));return p?c.json(p):c.json(notFound,404);});
 app.delete('/api/v1/people/:id',async c=>(await store.remove(c.req.param('id')))?c.body(null,204):c.json(notFound,404));
 app.put('/api/v1/people/:id/photo',async c=>{
  const data=new Uint8Array(await c.req.arrayBuffer());const type=c.req.header('Content-Type')?.split(';')[0];
  const png=data.length>=24 && Buffer.from(data.slice(0,8)).equals(Buffer.from([137,80,78,71,13,10,26,10]));
  const jpeg=data.length>=4 && data[0]===255 && data[1]===216 && data[2]===255 && data.at(-2)===255 && data.at(-1)===217;
  if(!((type==='image/png'&&png)||(type==='image/jpeg'&&jpeg)))return c.json({error:'Upload a PNG or JPEG image'},422);
  return await store.putPhoto(c.req.param('id'),data,type!)?c.json({has_photo:true}):c.json(notFound,404);
 });
 app.get('/api/v1/people/:id/photo',async c=>{const p=await store.getPhoto(c.req.param('id'));if(!p)return c.json(notFound,404);c.header('Content-Type',p.type);c.header('Content-Disposition','inline; filename="photo"');return c.body(p.body as any);});
 app.delete('/api/v1/people/:id/photo',async c=>await store.deletePhoto(c.req.param('id'))?c.body(null,204):c.json(notFound,404));
 app.notFound(c=>c.json({error:'Not found'},404));
 app.onError((e,c)=>{
  if(e instanceof ZodError)return c.json({error:'Invalid record fields',fields:e.issues.map(i=>({path:i.path.join('.'),message:i.message}))},422);
  if(e instanceof SyntaxError)return c.json({error:'Malformed JSON'},400);
  if(e.message==='date_order')return c.json({error:'Valid until must not precede valid from'},422);
  if(e.message==='content_type')return c.json({error:'Content-Type must be application/json'},415);
  console.error('Private Records request failed',e.name);
  return c.json({error:'Service temporarily unavailable'},503);
 });
 return app;
}
