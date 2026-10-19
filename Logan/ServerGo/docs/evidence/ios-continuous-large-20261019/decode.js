const fs=require('fs'),crypto=require('crypto');
const wire=fs.readFileSync(process.argv[2]),chunks=[];let pos=0,frames=0;
while(pos<wire.length){if(wire[pos]!==1||pos+6>wire.length)throw Error('bad frame header');let n=wire.readUInt32BE(pos+1);if(!n||n%16||pos+n+6>wire.length||wire[pos+n+5]!==0)throw Error('bad frame size/tail');let aes=crypto.createDecipheriv('aes-128-cbc',Buffer.from('0123456789012345'),Buffer.from('0123456789012345'));aes.setAutoPadding(false);chunks.push(aes.update(wire.subarray(pos+5,pos+5+n)),aes.final());pos+=n+6;frames++;}
fs.writeFileSync(process.argv[3],Buffer.concat(chunks));process.stdout.write(JSON.stringify({frames,wire_bytes:wire.length,decrypted_bytes:Buffer.concat(chunks).length}));
