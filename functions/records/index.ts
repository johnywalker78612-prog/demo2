import {createApp} from './app.js';
import {NeonStore} from './store.js';
const token=process.env.RECORDS_API_TOKEN;
if(!token)throw new Error('RECORDS_API_TOKEN is required');
export default createApp(new NeonStore('assets'),token);
