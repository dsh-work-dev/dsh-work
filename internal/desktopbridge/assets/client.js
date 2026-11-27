import { Stream as e } from "/wails/runtime.js";
(() => {
	if (globalThis.__DSH_WORK_ORIGINS__) return;
	let e = (e, t) => {
		try {
			let n = new URL(e, t);
			return n.username || n.password ? void 0 : n;
		} catch {}
	}, t = (t, n) => {
		let r = e(t, n);
		if (r) {
			if (r.protocol === "http:" || r.protocol === "https:") return r.origin;
			if (r.protocol === "wails:" && (r.host === "localhost" || r.host === "wails.localhost:48217")) return "wails://" + r.host;
		}
	};
	Object.defineProperty(globalThis, "__DSH_WORK_ORIGINS__", { value: Object.freeze({
		origin: t,
		same: (e, n) => {
			let r = t(e, n), i = t(n);
			return r !== void 0 && i !== void 0 && r === i;
		},
		gateway: (t, n) => {
			let r = e(n), i = e(t, n);
			return !r || !i || i.pathname !== "/api/remote.mux" ? !1 : r.protocol === "wails:" ? r.host === "wails.localhost:48217" && i.host === r.host && (i.protocol === "wails:" || i.protocol === "ws:") : ["http:", "https:"].includes(r.protocol) ? (i.protocol === "ws:" ? "http:" : i.protocol === "wss:" ? "https:" : i.protocol) === r.protocol && i.host === r.host : !1;
		}
	}) });
})(), globalThis.__DSH_WORK_ORIGINS__.origin;
var t = globalThis.__DSH_WORK_ORIGINS__.same, n = globalThis.__DSH_WORK_ORIGINS__.gateway;
//#endregion
//#region internal/desktopbridge/transport-core.ts
function r(e, t) {
	try {
		return n(e, t);
	} catch {
		return !1;
	}
}
function i(e, n) {
	try {
		let r = new URL(n), i = new URL(e, r);
		return t(i, r.href) && i.pathname === "/api/session/uploadFileBinary";
	} catch {
		return !1;
	}
}
function a(e, t, n) {
	let r, a = !1, o = () => {
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
		if (!i(l.url, n)) {
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
		if (r) {
			s({
				kind: "error",
				message: "background upload worker already has an active request"
			});
			return;
		}
		let u = new AbortController();
		r = u;
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
			r === u && (r = void 0);
		});
	};
	return new Proxy(e, {
		get(e, t) {
			if (t === "postMessage") return c;
			if (t === "terminate") return () => {
				r?.abort(), r = void 0, o();
			};
			let n = Reflect.get(e, t, e);
			return typeof n == "function" ? n.bind(e) : n;
		},
		set(e, t, n) {
			return Reflect.set(e, t, n, e);
		}
	});
}
function o(e, n) {
	try {
		let r = new URL(n), i = new URL(e, r), a = i.searchParams.getAll("sessionId"), o = i.searchParams.getAll("includeDescendants");
		if (!t(i, r.href) || i.pathname !== "/api/session.export" || a.length !== 1 || a[0] === "" || a[0].length > 4096 || o.length > 1) return;
		let s = o[0] ?? "false";
		return s !== "true" && s !== "false" ? void 0 : {
			sessionId: a[0],
			includeDescendants: s === "true"
		};
	} catch {
		return;
	}
}
function s(e) {
	return `/__work/session-export?${new URLSearchParams({
		sessionId: e.sessionId,
		includeDescendants: String(e.includeDescendants)
	}).toString()}`;
}
function c(e, t, n) {
	if (t !== "") return o(e, n);
}
//#endregion
//#region internal/desktopbridge/client.ts
var l = globalThis.fetch.bind(globalThis), u = globalThis.__WORK_GENERATION__, d = 64 * 1024;
async function f(n, r) {
	let i = { ...r }, a = i.uploadTotal, o = i.onUploadProgress;
	delete i.duplex, delete i.uploadTotal, delete i.onUploadProgress;
	let s = new Request(n, i), c = new URL(s.url);
	if (!t(c, location.href) || c.pathname.startsWith("/wails/")) return l(s);
	let f = e("worker-fetch");
	f.binaryType = "arraybuffer";
	let p = !1, m = !1, h, g, _, v, y, b = s.signal, x = () => {
		m = !0, b.removeEventListener("abort", S), _?.(/* @__PURE__ */ Error("Worker upload closed")), v?.(), y?.cancel().catch(() => {}), f.close();
	}, S = () => w(b.reason ?? new DOMException("Aborted", "AbortError")), C, w = (e) => {
		if (!m) {
			if (!p) C(e);
			else try {
				h?.error(e);
			} catch {}
			_?.(e), v?.(), x();
		}
	}, T = new Promise((e, t) => {
		C = t, f.onclose = () => {
			m || w(/* @__PURE__ */ Error("Worker connection closed"));
		}, f.onerror = () => w(/* @__PURE__ */ Error("Worker transport failed")), f.onmessage = (t) => {
			try {
				let n = new Uint8Array(t.data);
				switch (n[0]) {
					case 0: {
						if (p) throw Error("duplicate headers");
						let t = JSON.parse(new TextDecoder().decode(n.subarray(1))), r = new Headers();
						for (let [e, n] of Object.entries(t.headers)) for (let t of n) r.append(e, t);
						let i = s.method === "HEAD" || [
							204,
							205,
							304
						].includes(t.status), a = new ReadableStream({
							start(e) {
								h = e;
							},
							pull() {
								return new Promise((e) => {
									v = e, f.send(new Uint8Array([4]));
								});
							},
							cancel() {
								x();
							}
						}, { highWaterMark: 0 });
						p = !0, e(new Response(i ? null : a, {
							status: t.status,
							headers: r
						})), i && x();
						break;
					}
					case 1: {
						h.enqueue(n.slice(1));
						let e = v;
						v = void 0, e?.();
						break;
					}
					case 2:
						h?.close(), v?.(), x();
						break;
					case 3: {
						let e = g;
						g = void 0, _ = void 0, e?.();
						break;
					}
					default: throw Error("invalid Worker frame");
				}
			} catch (e) {
				w(e);
			}
		}, f.onopen = () => {
			let e = {};
			s.headers.forEach((t, n) => e[n] = [t]), f.send(new TextEncoder().encode(JSON.stringify({
				generation: u,
				url: c.pathname + c.search,
				method: s.method,
				headers: e,
				hasBody: !!s.body
			}))), (async () => {
				if (!s.body) return;
				let e = s.body.getReader();
				y = e;
				let t = 0;
				try {
					for (;;) {
						let { done: n, value: r } = await e.read();
						if (n) break;
						for (let e = 0; e < r.length; e += d) {
							if (m) throw Error("Worker upload closed");
							let n = r.subarray(e, e + d), i = new Uint8Array(n.length + 1);
							i[0] = 1, i.set(n, 1), await new Promise((e, t) => {
								g = e, _ = t, f.send(i);
							}), t += n.length, o?.({
								loaded: t,
								...a === void 0 ? {} : { total: a }
							});
						}
					}
					m || f.send(new Uint8Array([2]));
				} finally {
					await e.cancel().catch(() => {}), e.releaseLock();
				}
			})().catch(w);
		};
	});
	return b.addEventListener("abort", S, { once: !0 }), b.aborted && S(), T;
}
globalThis.WorkerBridge = {
	Stream: e,
	workerFetch: f
};
function p(e) {
	let t = window.parent === window ? void 0 : location.ancestorOrigins?.[0];
	t && t !== "null" && window.parent.postMessage({
		version: 1,
		type: "dsh-work/export-result",
		result: e
	}, t);
}
var m = /* @__PURE__ */ new Set(), h = globalThis.HTMLAnchorElement;
if (typeof h == "function") {
	let e = h.prototype.click;
	h.prototype.click = function() {
		let t = c(this.href, this.download, location.href);
		if (!t || !this.download) {
			e.call(this);
			return;
		}
		m.has(t.sessionId) || (m.add(t.sessionId), f(s(t), { method: "POST" }).then(async (e) => {
			let t = await e.json();
			t.result === "saved" ? p("saved") : t.result === "cancelled" ? p("cancelled") : p("error");
		}).catch(() => p("error")).finally(() => m.delete(t.sessionId)));
	};
}
var g = globalThis.Worker;
typeof g == "function" && (globalThis.Worker = new Proxy(g, { construct(e, t, n) {
	let r = Reflect.construct(e, t, n);
	return t[1]?.name === "dsh-file-upload" ? a(r, f, location.href) : r;
} }));
var _ = globalThis.WebSocket, v = class extends EventTarget {
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
				generation: u,
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
			for (let e = 0; e < r.length || e === 0; e += d) {
				let n = r.subarray(e, e + d), i = new Uint8Array(n.length + 1);
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
globalThis.WebSocket = new Proxy(_, { construct(e, t, n) {
	return r(t[0], location.href) ? new v(t[0], t[1]) : Reflect.construct(e, t, n);
} });
//#endregion
export { e as Stream, f as workerFetch };
