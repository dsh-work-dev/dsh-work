import {Socket, connect} from 'node:net';
import {createRequire} from 'node:module';

// The bridge path comes from the private launch sidecar written by Prepare,
// anchored to the application bundle, never from a page or request payload.
const require = createRequire(import.meta.url);
export async function openPrivateSocket(config, stopped = () => false) {
  if (process.platform !== 'darwin') return connect(config.pipe);
  const bridge = require(config.nativeBridge);
  const owner = await bridge.connect(config.pipe);
  if (stopped()) { bridge.close(owner); return null; }
  try {
    const socket = new Socket({fd: bridge.fd(owner), readable: true, writable: true});
    bridge.release(owner); // net.Socket now owns close, including destroy/error.
    return socket;
  } catch (error) {
    bridge.close(owner);
    throw error;
  }
}
