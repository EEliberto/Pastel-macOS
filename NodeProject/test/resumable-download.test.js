import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,readFile,rm,stat} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {resumeWorkspace} from '../src/resumable-download.js';

test('restart preserves partial bytes and pins the original version', async () => {
 const directory=await mkdtemp(path.join(os.tmpdir(),'pastel-resume-test-'));
 try {
  const input={directory,taskID:'app-1',email:'test@example.invalid',appID:'123',versionID:'',resolveVersion:async()=> '111'};
  const first=await resumeWorkspace(input);
  await writeFile(path.join(first.directory,'package.ipa.tmp'),'partial bytes');
  const restored=await resumeWorkspace({...input,resolveVersion:async()=>{assert.fail('must retain pinned version');}});
  assert.equal(restored.versionID,'111');
  assert.equal(await readFile(path.join(restored.directory,'package.ipa.tmp'),'utf8'),'partial bytes');
  await writeFile(path.join(restored.directory,'package.ipa'),'complete package');
  const output=await restored.finish(path.join(restored.directory,'package.ipa'));
  assert.equal(output,path.join(directory,'package.ipa'));
  assert.equal(await readFile(output,'utf8'),'complete package');
  await assert.rejects(stat(restored.directory),{code:'ENOENT'});
 } finally {await rm(directory,{recursive:true,force:true});}
});
test('accounts and task variants have isolated partial files',async()=>{
 const directory=await mkdtemp(path.join(os.tmpdir(),'pastel-resume-test-'));
 try{
  const input={directory,taskID:'one',email:'a@example.invalid',appID:'123',versionID:'111'};
  const a=await resumeWorkspace(input),b=await resumeWorkspace({...input,email:'b@example.invalid'}),c=await resumeWorkspace({...input,taskID:'two'});
  assert.equal(new Set([a.directory,b.directory,c.directory]).size,3);
  await assert.rejects(resumeWorkspace({...input,versionID:'222'}),/does not match/);
  await assert.rejects(a.finish(path.join(directory,'outside.ipa')),/Unexpected/);
 }finally{await rm(directory,{recursive:true,force:true});}
});
