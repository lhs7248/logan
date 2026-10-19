/* Execute unchanged SDK TypeScript with a simulated IndexedDB and captured XHR. */
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const ts = require('typescript');
const [sdkRoot, out, publicKeyPath, configuredMarker] = process.argv.slice(2);
require.extensions['.ts'] = (module, filename) => {
  const code = ts.transpileModule(fs.readFileSync(filename, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2018, esModuleInterop: false }
  }).outputText;
  module._compile(code, filename);
};
global.window = global;
global.document = { readyState: 'complete' };
global.navigator = { appName: 'Netscape', userAgent: 'Node SDK fixture' };
global.addEventListener = () => {};
global.removeEventListener = () => {};
global.crypto = crypto.webcrypto;
require('fake-indexeddb/auto');
const captured = [];
global.XMLHttpRequest = class {
  open(method, url) { this.method = method; this.url = url; }
  setRequestHeader() {}
  send(body) {
    captured.push(JSON.parse(body));
    this.status = 200; this.readyState = 4; this.responseText = '{"code":200}';
    queueMicrotask(() => this.onreadystatechange());
  }
};
const sdk = require(path.join(sdkRoot, 'src/index.ts'));
const marker = configuredMarker || 'SDK_CHAIN_WEB_20261016 中文 🌏 + percent%';
(async () => {
  let resolveSave, rejectSave;
  const saved = new Promise((res, rej) => { resolveSave = res; rejectSave = rej; });
  sdk.initConfig({ publicKey: fs.readFileSync(publicKeyPath, 'utf8'), dbName: 'sdk-chain-fixture',
    succHandler: resolveSave, errorHandler: rejectSave, reportUrl: 'http://fixture.invalid/logan/web/upload' });
  sdk.logWithEncryption(marker, 7);
  await Promise.race([saved, new Promise((_, rej) => setTimeout(() => rej(Error('SDK save timeout')), 10000))]);
  const day = require(path.join(sdkRoot, 'src/lib/utils.ts')).dateFormat2Day(new Date());
  const result = await sdk.report({ fromDayString: day, toDayString: day, deviceId: 'sdk-chain-device', webSource: 'sdk-fixture' });
  if (captured.length !== 1) throw Error('Expected one actual SDK report, got ' + captured.length + ': ' + JSON.stringify(result));
  fs.writeFileSync(path.join(out, 'web-upload.json'), JSON.stringify(captured[0], null, 2));
  fs.writeFileSync(path.join(out, 'web-expected.json'), JSON.stringify({ marker, type: 7, reportResult: result }, null, 2));
  console.log('WebSDK index → saveLog → IndexedDB emulator → report → captured XHR succeeded');
})().then(() => process.exit(0), e => { console.error(e); process.exit(1); });
