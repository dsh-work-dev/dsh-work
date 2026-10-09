import { Stream as e } from "/wails/runtime.js";
//#region internal/desktopbridge/transport-core.ts
function t(e, t) {
	try {
		let n = new URL(t), r = new URL(e, n), i = r.origin.replace(/^ws:/, "http:").replace(/^wss:/, "https:");
		return r.username === "" && r.password === "" && i === n.origin && r.pathname === "/api/remote.mux";
	} catch {
		return !1;
	}
}
function n(e, t) {
	try {
		let n = new URL(t), r = new URL(e, n);
		return r.origin === n.origin && r.pathname === "/api/session/uploadFileBinary";
	} catch {
		return !1;
	}
}
function r(e, t, r) {
	let i, a = !1, o = () => {
		a || (a = !0, e.terminate());
	}, s = (t) => {
		let n = new MessageEvent("message", { data: t });
		typeof e.dispatchEvent == "function" ? e.dispatchEvent(n) : e.onmessage?.call(e, n);
	}, c = (a, c) => {
		let l = a;
		if (typeof l?.url != "string" && !(l?.url instanceof URL)) {
			e.postMessage(a, c);
			return;
		}
		if (!n(l.url, r)) {
			e.postMessage(a, c);
			return;
		}
		if (o(), !(l.body instanceof Blob) && !(l.body instanceof ReadableStream)) {
			s({
				kind: "error",
				message: "background upload worker received an invalid body"
			});
			return;
		}
		if (i) {
			s({
				kind: "error",
				message: "background upload worker already has an active request"
			});
			return;
		}
		let u = new AbortController();
		i = u;
		let d = l.body instanceof Blob ? l.body.size : void 0, f = {
			method: "POST",
			headers: l.headers ?? {},
			credentials: "include",
			body: l.body,
			signal: u.signal,
			...l.body instanceof ReadableStream ? { duplex: "half" } : {},
			...d === void 0 ? {} : { uploadTotal: d },
			onUploadProgress: (e) => s({
				kind: "progress",
				...e
			})
		};
		t(l.url, f).then(async (e) => {
			let t = await e.text();
			u.signal.aborted || s({
				kind: "complete",
				status: e.status,
				body: t
			});
		}).catch((e) => {
			u.signal.aborted || s({
				kind: "error",
				message: e instanceof Error ? e.message : String(e)
			});
		}).finally(() => {
			i === u && (i = void 0);
		});
	};
	return new Proxy(e, {
		get(e, t) {
			return t === "postMessage" ? c : t === "terminate" ? () => {
				i?.abort(), i = void 0, o();
			} : Reflect.get(e, t, e);
		},
		set(e, t, n) {
			return Reflect.set(e, t, n, e);
		}
	});
}
//#endregion
//#region internal/desktopbridge/client.ts
var i = globalThis.fetch.bind(globalThis), a = globalThis.__WORK_GENERATION__, o = 64 * 1024;
async function s(t, n) {
	let r = { ...n }, s = r.uploadTotal, c = r.onUploadProgress;
	delete r.duplex, delete r.uploadTotal, delete r.onUploadProgress;
	let l = new Request(t, r), u = new URL(l.url);
	if (u.origin !== location.origin || u.pathname.startsWith("/wails/")) return i(l);
	let d = e("worker-fetch");
	d.binaryType = "arraybuffer";
	let f = !1, p = !1, m, h, g, _, v, y = l.signal, b = () => {
		p = !0, y.removeEventListener("abort", x), g?.(/* @__PURE__ */ Error("Worker upload closed")), _?.(), v?.cancel().catch(() => {}), d.close();
	}, x = () => C(y.reason ?? new DOMException("Aborted", "AbortError")), S, C = (e) => {
		if (!p) {
			if (!f) S(e);
			else try {
				m?.error(e);
			} catch {}
			g?.(e), _?.(), b();
		}
	}, w = new Promise((e, t) => {
		S = t, d.onclose = () => {
			p || C(/* @__PURE__ */ Error("Worker connection closed"));
		}, d.onerror = () => C(/* @__PURE__ */ Error("Worker transport failed")), d.onmessage = (t) => {
			try {
				let n = new Uint8Array(t.data);
				switch (n[0]) {
					case 0: {
						if (f) throw Error("duplicate headers");
						let t = JSON.parse(new TextDecoder().decode(n.subarray(1))), r = new Headers();
						for (let [e, n] of Object.entries(t.headers)) for (let t of n) r.append(e, t);
						let i = l.method === "HEAD" || [
							204,
							205,
							304
						].includes(t.status), a = new ReadableStream({
							start(e) {
								m = e;
							},
							pull() {
								return new Promise((e) => {
									_ = e, d.send(new Uint8Array([4]));
								});
							},
							cancel() {
								b();
							}
						}, { highWaterMark: 0 });
						f = !0, e(new Response(i ? null : a, {
							status: t.status,
							headers: r
						})), i && b();
						break;
					}
					case 1: {
						m.enqueue(n.slice(1));
						let e = _;
						_ = void 0, e?.();
						break;
					}
					case 2:
						m?.close(), _?.(), b();
						break;
					case 3: {
						let e = h;
						h = void 0, g = void 0, e?.();
						break;
					}
					default: throw Error("invalid Worker frame");
				}
			} catch (e) {
				C(e);
			}
		}, d.onopen = () => {
			let e = {};
			l.headers.forEach((t, n) => e[n] = [t]), d.send(new TextEncoder().encode(JSON.stringify({
				generation: a,
				url: u.pathname + u.search,
				method: l.method,
				headers: e,
				hasBody: !!l.body
			}))), (async () => {
				if (!l.body) return;
				let e = l.body.getReader();
				v = e;
				let t = 0;
				try {
					for (;;) {
						let { done: n, value: r } = await e.read();
						if (n) break;
						for (let e = 0; e < r.length; e += o) {
							if (p) throw Error("Worker upload closed");
							let n = r.subarray(e, e + o), i = new Uint8Array(n.length + 1);
							i[0] = 1, i.set(n, 1), await new Promise((e, t) => {
								h = e, g = t, d.send(i);
							}), t += n.length, c?.({
								loaded: t,
								...s === void 0 ? {} : { total: s }
							});
						}
					}
					p || d.send(new Uint8Array([2]));
				} finally {
					await e.cancel().catch(() => {}), e.releaseLock();
				}
			})().catch(C);
		};
	});
	return y.addEventListener("abort", x, { once: !0 }), y.aborted && x(), w;
}
globalThis.WorkerBridge = {
	Stream: e,
	workerFetch: s
};
var c = globalThis.Worker;
typeof c == "function" && (globalThis.Worker = new Proxy(c, { construct(e, t, n) {
	let i = Reflect.construct(e, t, n);
	return t[1]?.name === "dsh-file-upload" ? r(i, s, location.href) : i;
} }));
var l = globalThis.WebSocket, u = class extends EventTarget {
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
	constructor(t, n) {
		super(), this.CONNECTING = 0, this.OPEN = 1, this.CLOSING = 2, this.CLOSED = 3, this.readyState = 0, this.binaryType = "blob", this.bufferedAmount = 0, this.extensions = "", this.protocol = "", this.onopen = null, this.onclose = null, this.onerror = null, this.onmessage = null, this.socket = e("worker-websocket"), this.queue = Promise.resolve(), this.parts = [], this.size = 0, this.kind = 2, this.url = new URL(t, location.href).href, this.socket.binaryType = "arraybuffer", this.socket.onopen = () => {
			let e = new URL(this.url);
			this.socket.send(new TextEncoder().encode(JSON.stringify({
				url: e.pathname + e.search,
				generation: a,
				protocols: typeof n == "string" ? [n] : n ?? []
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
			let r = typeof e == "string" ? new TextEncoder().encode(e) : e instanceof Blob ? new Uint8Array(await e.arrayBuffer()) : ArrayBuffer.isView(e) ? new Uint8Array(e.buffer, e.byteOffset, e.byteLength) : new Uint8Array(e);
			for (let e = 0; e < r.length || e === 0; e += o) {
				let n = r.subarray(e, e + o), i = new Uint8Array(n.length + 1);
				i[0] = t, i.set(n, 1), await this.write(i);
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
globalThis.WebSocket = new Proxy(l, { construct(e, n, r) {
	return t(n[0], location.href) ? new u(n[0], n[1]) : Reflect.construct(e, n, r);
} });
//#endregion
export { e as Stream, s as workerFetch };
