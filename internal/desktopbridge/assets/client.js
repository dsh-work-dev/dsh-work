import { Stream as e } from "/wails/runtime.js";
//#region internal/desktopbridge/account_feedback.ts
var t, n;
function r(e) {
	if (!e || typeof e != "object") return !1;
	let t = e;
	return t.type === "server-response" && t.result?.ok === !0;
}
function i(e) {
	let r = document.body ?? document.documentElement;
	r && ((!t || !t.isConnected) && (t = document.createElement("div"), t.setAttribute("role", "status"), t.setAttribute("aria-live", "polite"), Object.assign(t.style, {
		position: "fixed",
		top: "20px",
		right: "20px",
		zIndex: "2147483647",
		maxWidth: "min(360px, calc(100vw - 40px))",
		padding: "12px 16px",
		borderRadius: "10px",
		background: "rgba(24, 24, 27, 0.96)",
		color: "#fff",
		boxShadow: "0 8px 24px rgba(0, 0, 0, 0.22)",
		font: "500 14px/1.5 system-ui, sans-serif",
		pointerEvents: "none",
		opacity: "0",
		transition: "opacity 120ms ease"
	}), r.appendChild(t)), n !== void 0 && window.clearTimeout(n), t.setAttribute("role", e === "error" ? "alert" : "status"), t.textContent = a(e), t.style.opacity = "1", e !== "pending" && (n = window.setTimeout(() => {
		t && (t.style.opacity = "0");
	}, 3e3)));
}
function a(e) {
	return (document.documentElement.lang || navigator.language).toLowerCase().startsWith("zh") ? e === "pending" ? "正在退出登录…" : e === "success" ? "已退出登录" : "退出登录失败，请重试" : e === "pending" ? "Signing out…" : e === "success" ? "Signed out" : "Could not sign out. Try again.";
}
function o() {
	i("pending");
}
async function s(e) {
	if (!e.ok) return !1;
	try {
		return r(await e.clone().json());
	} catch {
		return !1;
	}
}
async function c(e) {
	let t = e === !0;
	e instanceof Response && (t = await s(e)), i(t ? "success" : "error");
}
//#endregion
//#region internal/desktopbridge/client.ts
var l = globalThis.fetch.bind(globalThis), u = globalThis.__WORK_GENERATION__, d = 64 * 1024;
globalThis.dshDesktop ??= {};
async function f(t, n) {
	let r = new Request(t, n), i = new URL(r.url);
	if (i.origin !== location.origin || i.pathname.startsWith("/wails/")) return l(r);
	let a = e("worker-fetch");
	a.binaryType = "arraybuffer";
	let s = !1, f = !1, p, m, h, g, _, v = r.signal, y = () => {
		f = !0, v.removeEventListener("abort", b), h?.(/* @__PURE__ */ Error("Worker upload closed")), g?.(), _?.cancel().catch(() => {}), a.close();
	}, b = () => S(v.reason ?? new DOMException("Aborted", "AbortError")), x, S = (e) => {
		if (!f) {
			if (!s) x(e);
			else try {
				p?.error(e);
			} catch {}
			h?.(e), g?.(), y();
		}
	}, C = new Promise((e, t) => {
		x = t, a.onclose = () => {
			f || S(/* @__PURE__ */ Error("Worker connection closed"));
		}, a.onerror = () => S(/* @__PURE__ */ Error("Worker transport failed")), a.onmessage = (t) => {
			try {
				let n = new Uint8Array(t.data);
				switch (n[0]) {
					case 0: {
						if (s) throw Error("duplicate headers");
						let t = JSON.parse(new TextDecoder().decode(n.subarray(1))), i = new Headers();
						for (let [e, n] of Object.entries(t.headers)) for (let t of n) i.append(e, t);
						let o = r.method === "HEAD" || [
							204,
							205,
							304
						].includes(t.status), c = new ReadableStream({
							start(e) {
								p = e;
							},
							pull() {
								return new Promise((e) => {
									g = e, a.send(new Uint8Array([4]));
								});
							},
							cancel() {
								y();
							}
						}, { highWaterMark: 0 });
						s = !0, e(new Response(o ? null : c, {
							status: t.status,
							headers: i
						})), o && y();
						break;
					}
					case 1: {
						p.enqueue(n.slice(1));
						let e = g;
						g = void 0, e?.();
						break;
					}
					case 2:
						p?.close(), g?.(), y();
						break;
					case 3: {
						let e = m;
						m = void 0, h = void 0, e?.();
						break;
					}
					default: throw Error("invalid Worker frame");
				}
			} catch (e) {
				S(e);
			}
		}, a.onopen = () => {
			let e = {};
			r.headers.forEach((t, n) => e[n] = [t]), a.send(new TextEncoder().encode(JSON.stringify({
				generation: u,
				url: i.pathname + i.search,
				method: r.method,
				headers: e,
				hasBody: !!r.body
			}))), (async () => {
				if (!r.body) return;
				let e = r.body.getReader();
				_ = e;
				try {
					for (;;) {
						let { done: t, value: n } = await e.read();
						if (t) break;
						for (let e = 0; e < n.length; e += d) {
							if (f) throw Error("Worker upload closed");
							let t = n.subarray(e, e + d), r = new Uint8Array(t.length + 1);
							r[0] = 1, r.set(t, 1), await new Promise((e, t) => {
								m = e, h = t, a.send(r);
							});
						}
					}
					f || a.send(new Uint8Array([2]));
				} finally {
					await e.cancel().catch(() => {}), e.releaseLock();
				}
			})().catch(S);
		};
	});
	v.addEventListener("abort", b, { once: !0 }), v.aborted && b();
	let w = r.method === "POST" && i.pathname === "/api/account/signOut";
	w && o();
	let T;
	try {
		T = await C;
	} catch (e) {
		throw w && c(!1), e;
	}
	return w && c(T), T;
}
globalThis.fetch = f, globalThis.WorkerBridge = {
	Stream: e,
	workerFetch: f
}, globalThis.__DSH_TRANSPORT__ = { ownsHost: !0 };
var p = globalThis.WebSocket, m = class extends EventTarget {
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
globalThis.WebSocket = new Proxy(p, { construct(e, t) {
	let n = new URL(t[0], location.href), r = new URL(location.href);
	return n.host === r.host ? new m(t[0], t[1]) : Reflect.construct(e, t);
} });
var h = /* @__PURE__ */ new Map();
function g(e) {
	let t = new URL(e, location.href);
	if (!["http:", "https:"].includes(t.protocol)) return;
	let n = Date.now();
	h.forEach((e, t) => {
		n - e > 3e3 && h.delete(t);
	}), !(n - (h.get(t.href) ?? 0) < 3e3) && (h.set(t.href, n), f("/__work/external?url=" + encodeURIComponent(t.href), { method: "POST" }).then((e) => {
		e.ok || console.warn("Could not open the external browser.");
	}).catch(() => console.warn("Could not open the external browser.")));
}
document.addEventListener("click", (e) => {
	let t = e.target?.closest?.("a[href]");
	if (!t) return;
	let n = new URL(t.href, location.href);
	n.origin !== location.origin && (e.preventDefault(), g(n.href));
}, !0);
var _ = window.open.bind(window);
window.open = ((e, t, n) => {
	if (e) {
		let t = new URL(e, location.href);
		if (t.origin !== location.origin) return g(t.href), null;
	}
	return _(e, t, n);
});
//#endregion
export { e as Stream, f as workerFetch };
