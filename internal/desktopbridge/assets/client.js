import { Stream as e } from "/wails/runtime.js";
//#region internal/desktopbridge/client.ts
var t = globalThis.fetch.bind(globalThis), n = globalThis.__WORK_GENERATION__, r = 64 * 1024;
async function i(i, a) {
	let o = new Request(i, a), s = new URL(o.url);
	if (s.origin !== location.origin || s.pathname.startsWith("/wails/")) return t(o);
	let c = e("worker-fetch");
	c.binaryType = "arraybuffer";
	let l = !1, u = !1, d, f, p, m, h, g = o.signal, _ = () => {
		u = !0, g.removeEventListener("abort", v), p?.(/* @__PURE__ */ Error("Worker upload closed")), m?.(), h?.cancel().catch(() => {}), c.close();
	}, v = () => b(g.reason ?? new DOMException("Aborted", "AbortError")), y, b = (e) => {
		if (!u) {
			if (!l) y(e);
			else try {
				d?.error(e);
			} catch {}
			p?.(e), m?.(), _();
		}
	}, x = new Promise((e, t) => {
		y = t, c.onclose = () => {
			u || b(/* @__PURE__ */ Error("Worker connection closed"));
		}, c.onerror = () => b(/* @__PURE__ */ Error("Worker transport failed")), c.onmessage = (t) => {
			try {
				let n = new Uint8Array(t.data);
				switch (n[0]) {
					case 0: {
						if (l) throw Error("duplicate headers");
						let t = JSON.parse(new TextDecoder().decode(n.subarray(1))), r = new Headers();
						for (let [e, n] of Object.entries(t.headers)) for (let t of n) r.append(e, t);
						let i = o.method === "HEAD" || [
							204,
							205,
							304
						].includes(t.status), a = new ReadableStream({
							start(e) {
								d = e;
							},
							pull() {
								return new Promise((e) => {
									m = e, c.send(new Uint8Array([4]));
								});
							},
							cancel() {
								_();
							}
						}, { highWaterMark: 0 });
						l = !0, e(new Response(i ? null : a, {
							status: t.status,
							headers: r
						})), i && _();
						break;
					}
					case 1: {
						d.enqueue(n.slice(1));
						let e = m;
						m = void 0, e?.();
						break;
					}
					case 2:
						d?.close(), m?.(), _();
						break;
					case 3: {
						let e = f;
						f = void 0, p = void 0, e?.();
						break;
					}
					default: throw Error("invalid Worker frame");
				}
			} catch (e) {
				b(e);
			}
		}, c.onopen = () => {
			let e = {};
			o.headers.forEach((t, n) => e[n] = [t]), c.send(new TextEncoder().encode(JSON.stringify({
				generation: n,
				url: s.pathname + s.search,
				method: o.method,
				headers: e,
				hasBody: !!o.body
			}))), (async () => {
				if (!o.body) return;
				let e = o.body.getReader();
				h = e;
				try {
					for (;;) {
						let { done: t, value: n } = await e.read();
						if (t) break;
						for (let e = 0; e < n.length; e += r) {
							if (u) throw Error("Worker upload closed");
							let t = n.subarray(e, e + r), i = new Uint8Array(t.length + 1);
							i[0] = 1, i.set(t, 1), await new Promise((e, t) => {
								f = e, p = t, c.send(i);
							});
						}
					}
					u || c.send(new Uint8Array([2]));
				} finally {
					await e.cancel().catch(() => {}), e.releaseLock();
				}
			})().catch(b);
		};
	});
	return g.addEventListener("abort", v, { once: !0 }), g.aborted && v(), x;
}
globalThis.fetch = i, globalThis.WorkerBridge = {
	Stream: e,
	workerFetch: i
}, globalThis.__DSH_TRANSPORT__ = {
	ownsHost: !0,
	async *openStream(e, t, n) {
		let r = await i(new URL("/.dsh/remote-stream", location.href), {
			method: "POST",
			headers: { "content-type": "application/json" },
			body: JSON.stringify({
				endpoint: e,
				payload: t
			}),
			signal: n
		});
		if (!r.ok || !r.body) throw Error(`Worker stream HTTP ${r.status}`);
		let a = r.body.getReader(), o = new TextDecoder(), s = "";
		try {
			for (;;) {
				let { done: e, value: t } = await a.read();
				s += o.decode(t, { stream: !e });
				let n;
				for (; (n = s.indexOf("\n")) >= 0;) {
					let e = s.slice(0, n);
					s = s.slice(n + 1), e && (yield JSON.parse(e));
				}
				if (s.length > 16 * 1024 * 1024) throw Error("Worker stream item too large");
				if (e) break;
			}
			s && (yield JSON.parse(s));
		} finally {
			await a.cancel().catch(() => {}), a.releaseLock();
		}
	}
};
var a = globalThis.WebSocket, o = class extends EventTarget {
	static {
		this.CONNECTING = 0;
	}
	static {
		this.OPEN = 1;
	}
	static {
		this.CLOSING = 2;
	}
	static {
		this.CLOSED = 3;
	}
	constructor(t, r) {
		super(), this.CONNECTING = 0, this.OPEN = 1, this.CLOSING = 2, this.CLOSED = 3, this.readyState = 0, this.binaryType = "blob", this.bufferedAmount = 0, this.extensions = "", this.protocol = "", this.onopen = null, this.onclose = null, this.onerror = null, this.onmessage = null, this.socket = e("worker-websocket"), this.queue = Promise.resolve(), this.parts = [], this.size = 0, this.kind = 2, this.url = new URL(t, location.href).href, this.socket.binaryType = "arraybuffer", this.socket.onopen = () => {
			let e = new URL(this.url);
			this.socket.send(new TextEncoder().encode(JSON.stringify({
				url: e.pathname + e.search,
				generation: n,
				protocols: typeof r == "string" ? [r] : r ?? []
			})));
		}, this.socket.onmessage = (e) => {
			let t = new Uint8Array(e.data);
			if (t[0] === 0) this.protocol = new TextDecoder().decode(t.subarray(1)), this.readyState = 1, this.emit("open", new Event("open")), this.socket.send(new Uint8Array([4]));
			else if (t[0] === 6) {
				let e = JSON.parse(new TextDecoder().decode(t.subarray(1)));
				this.closed = new CloseEvent("close", {
					code: e.code,
					reason: e.reason,
					wasClean: !0
				}), this.socket.close();
			} else if (t[0] === 3) {
				let e = this.ack;
				this.ack = void 0, this.rejectAck = void 0, e?.();
			} else if (t[0] === 1 || t[0] === 2) {
				if (this.kind = t[0], this.parts.push(t.slice(1)), this.size += t.length - 1, this.size > 16 * 1024 * 1024) {
					this.close();
					return;
				}
				this.socket.send(new Uint8Array([4]));
			} else if (t[0] === 5) {
				let e = new Uint8Array(this.size), t = 0;
				for (let n of this.parts) e.set(n, t), t += n.length;
				this.parts = [], this.size = 0;
				let n = this.kind === 1 ? new TextDecoder().decode(e) : this.binaryType === "arraybuffer" ? e.buffer : new Blob([e]);
				this.emit("message", new MessageEvent("message", {
					data: n,
					origin: location.origin
				})), this.socket.send(new Uint8Array([4]));
			}
		}, this.socket.onerror = () => this.emit("error", new Event("error")), this.socket.onclose = () => {
			this.readyState = 3, this.rejectAck?.(/* @__PURE__ */ Error("WebSocket closed")), this.emit("close", this.closed ?? new CloseEvent("close", {
				code: 1006,
				wasClean: !1
			}));
		};
	}
	emit(e, t) {
		this.dispatchEvent(t);
		try {
			this["on" + e]?.(t);
		} catch (e) {
			reportError(e);
		}
	}
	send(e) {
		if (this.readyState === 0) throw new DOMException("WebSocket is connecting", "InvalidStateError");
		if (this.readyState !== 1) return;
		let t = typeof e == "string" ? 1 : 2;
		typeof e != "string" && !(e instanceof Blob) && (e = ArrayBuffer.isView(e) ? new Uint8Array(e.buffer, e.byteOffset, e.byteLength).slice().buffer : new Uint8Array(e).slice().buffer);
		let n = typeof e == "string" ? new TextEncoder().encode(e).length : e instanceof Blob ? e.size : e.byteLength;
		this.bufferedAmount += n, this.queue = this.queue.then(async () => {
			let i = typeof e == "string" ? new TextEncoder().encode(e) : e instanceof Blob ? new Uint8Array(await e.arrayBuffer()) : ArrayBuffer.isView(e) ? new Uint8Array(e.buffer, e.byteOffset, e.byteLength) : new Uint8Array(e);
			for (let e = 0; e < i.length || e === 0; e += r) {
				let n = i.subarray(e, e + r), a = new Uint8Array(n.length + 1);
				a[0] = t, a.set(n, 1), await this.write(a);
			}
			await this.write(new Uint8Array([5])), this.bufferedAmount -= n;
		}).catch(() => this.close());
	}
	write(e) {
		return new Promise((t, n) => {
			if (this.readyState !== 1) {
				n(/* @__PURE__ */ Error("WebSocket closed"));
				return;
			}
			this.ack = t, this.rejectAck = n, this.socket.send(e);
		});
	}
	close(e = 1e3, t = "") {
		if (e !== 1e3 && (e < 3e3 || e > 4999)) throw new DOMException("Invalid close code", "InvalidAccessError");
		if (new TextEncoder().encode(t).length > 123) throw new DOMException("Close reason too long", "SyntaxError");
		if (this.readyState >= 2) return;
		if (this.readyState === 0) {
			this.readyState = 2, this.socket.close();
			return;
		}
		this.readyState = 2;
		let n = new TextEncoder().encode(JSON.stringify({
			code: e,
			reason: t
		})), r = new Uint8Array(n.length + 1);
		r[0] = 6, r.set(n, 1), this.socket.send(r);
	}
};
globalThis.WebSocket = new Proxy(a, { construct(e, t) {
	let n = new URL(t[0], location.href), r = new URL(location.href);
	return n.host === r.host ? new o(t[0], t[1]) : Reflect.construct(e, t);
} });
function s(e) {
	let t = new URL(e, location.href);
	["http:", "https:"].includes(t.protocol) && i("/__work/external?url=" + encodeURIComponent(t.href), { method: "POST" }).catch(() => {});
}
document.addEventListener("click", (e) => {
	let t = e.target?.closest?.("a[href]");
	if (!t) return;
	let n = new URL(t.href, location.href);
	n.origin !== location.origin && (e.preventDefault(), s(n.href));
}, !0);
var c = window.open.bind(window);
window.open = ((e, t, n) => {
	if (e) {
		let t = new URL(e, location.href);
		if (t.origin !== location.origin) return s(t.href), null;
	}
	return c(e, t, n);
});
//#endregion
export { e as Stream, i as workerFetch };
