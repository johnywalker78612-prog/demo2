import { z } from 'zod';
const text = z.string().trim().max(250);
const date = z.union([z.literal(''), z.string().regex(/^\d{4}-\d{2}-\d{2}$/).refine(v => {const d=new Date(v+'T00:00:00Z');return !isNaN(d.getTime()) && d.toISOString().slice(0,10)===v;}, 'Invalid calendar date')]);
export const fields = z.object({
 first_name:text.min(1), last_name:text.optional(), email:z.union([z.literal(''),z.email().max(250)]).optional(),
 phone_number:text.refine(v=>!v || (/^[+\d () .-]+$/.test(v) && v.replace(/\D/g,'').length>=7 && v.replace(/\D/g,'').length<=15),'Invalid phone number').optional(),
 date_of_birth:date.refine(v=>!v || v<=new Date().toISOString().slice(0,10),'Birth date is in the future').optional(),
 nationality:text.optional(),passport_number:text.optional(),recorded_status:text.optional(),accompanied_by:text.optional(),
 valid_from:date.optional(),valid_until:date.optional(),entries_allowed:text.optional(),national_insurance_number:text.optional(),
}).strict();
export function validatePerson(input: unknown) {
 const person=fields.parse(input);
 if(person.valid_from && person.valid_until && person.valid_until<person.valid_from) throw new Error('date_order');
 return person;
}
export const patchFields=fields.partial().refine(v=>Object.keys(v).length>0,'At least one field is required');
export const lookupFields=z.object({id:text.min(1),date_of_birth:date.refine(v=>!!v && v<=new Date().toISOString().slice(0,10),'A valid birth date is required')}).strict();
export type PersonInput=z.infer<typeof fields>;
export type Person=PersonInput & {id:string;unverified:true;has_photo:boolean;created_at:string;updated_at:string};
