// UI correlation only; not an authentication token. Works on plain HTTP where
// crypto.randomUUID is unavailable. Mutations must never depend on Web Crypto.
let sequence=0;
const instance=Math.random().toString(36).slice(2);
export function operationID(){return `op-${Date.now().toString(36)}-${instance}-${++sequence}`}
