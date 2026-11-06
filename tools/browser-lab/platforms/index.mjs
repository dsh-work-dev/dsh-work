export function platformSupport(platform = process.platform) {
 if (platform === 'win32') return {implemented: true, name: 'windows', module: './windows.mjs'};
 if (platform === 'darwin') return {implemented: true, name: 'macos', module: './posix.mjs'};
 if (platform === 'linux') return {implemented: true, name: 'linux-x11', module: './posix.mjs'};
 return {implemented: false, name: platform, reason: 'No native adapter registered.'};
}

export function validateSession(session) {
 if (session?.schemaVersion !== 1 || !session.runId || !session.dataRoot || !session.evidenceRoot || !session.token || session.token.length < 24) throw Error('Invalid private lab session');
 for (const [name, port] of Object.entries({fixture:19478, cef:19479, shell:19480, control:19481, gateway:19482, backend:19483, artifact:19484})) {
  if (session.endpoints?.[name] !== `http://127.0.0.1:${port}`) throw Error('Unexpected lab endpoint: ' + name);
 }
 return session;
}
