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
			if (t === "postMessage") return c;
			if (t === "terminate") return () => {
				i?.abort(), i = void 0, o();
			};
			let n = Reflect.get(e, t, e);
			return typeof n == "function" ? n.bind(e) : n;
		},
		set(e, t, n) {
			return Reflect.set(e, t, n, e);
		}
	});
}
function i(e, t) {
	try {
		let n = new URL(t), r = new URL(e, n), i = r.searchParams.getAll("sessionId"), a = r.searchParams.getAll("includeDescendants");
		if (r.username !== "" || r.password !== "" || r.origin !== n.origin || r.pathname !== "/api/session.export" || i.length !== 1 || i[0] === "" || i[0].length > 4096 || a.length > 1) return;
		let o = a[0] ?? "false";
		return o !== "true" && o !== "false" ? void 0 : {
			sessionId: i[0],
			includeDescendants: o === "true"
		};
	} catch {
		return;
	}
}
function a(e) {
	return `/__work/session-export?${new URLSearchParams({
		sessionId: e.sessionId,
		includeDescendants: String(e.includeDescendants)
	}).toString()}`;
}
function o(e, t, n) {
	if (t !== "") return i(e, n);
}
//#endregion
//#region internal/desktopbridge/client.ts
var s = globalThis.fetch.bind(globalThis), c = globalThis.__WORK_GENERATION__, l = 64 * 1024;
async function u(t, n) {
	let r = { ...n }, i = r.uploadTotal, a = r.onUploadProgress;
	delete r.duplex, delete r.uploadTotal, delete r.onUploadProgress;
	let o = new Request(t, r), u = new URL(o.url);
	if (u.origin !== location.origin || u.pathname.startsWith("/wails/")) return s(o);
	let d = e("worker-fetch");
	d.binaryType = "arraybuffer";
	let f = !1, p = !1, m, h, g, _, v, y = o.signal, b = () => {
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
						let i = o.method === "HEAD" || [
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
			o.headers.forEach((t, n) => e[n] = [t]), d.send(new TextEncoder().encode(JSON.stringify({
				generation: c,
				url: u.pathname + u.search,
				method: o.method,
				headers: e,
				hasBody: !!o.body
			}))), (async () => {
				if (!o.body) return;
				let e = o.body.getReader();
				v = e;
				let t = 0;
				try {
					for (;;) {
						let { done: n, value: r } = await e.read();
						if (n) break;
						for (let e = 0; e < r.length; e += l) {
							if (p) throw Error("Worker upload closed");
							let n = r.subarray(e, e + l), o = new Uint8Array(n.length + 1);
							o[0] = 1, o.set(n, 1), await new Promise((e, t) => {
								h = e, g = t, d.send(o);
							}), t += n.length, a?.({
								loaded: t,
								...i === void 0 ? {} : { total: i }
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
	workerFetch: u
};
var d, f;
function p(e) {
	let t = document.body ?? document.documentElement;
	if (!t) return;
	let n = (document.documentElement?.lang || navigator.language || "en").toLowerCase(), r = n.startsWith("zh"), i = n.startsWith("ja"), a = e === "saved" ? r ? "会话日志已保存" : i ? "セッションログを保存しました" : "Session log saved." : e === "cancelled" ? r ? "已取消导出" : i ? "エクスポートをキャンセルしました" : "Export cancelled." : r ? "无法导出会话日志，请重试。" : i ? "セッションログをエクスポートできませんでした。もう一度お試しください。" : "Could not export the session log. Try again.";
	(!d || !d.isConnected) && (d = document.createElement("div"), Object.assign(d.style, {
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
	}), t.appendChild(d)), d.setAttribute("role", e === "error" ? "alert" : "status"), d.setAttribute("aria-live", e === "error" ? "assertive" : "polite"), d.textContent = a, d.style.opacity = "1", f !== void 0 && window.clearTimeout(f), f = window.setTimeout(() => {
		d && (d.style.opacity = "0");
	}, 3500);
}
var m = /* @__PURE__ */ new Set(), h = globalThis.HTMLAnchorElement;
if (typeof h == "function") {
	let e = h.prototype.click;
	h.prototype.click = function() {
		let t = o(this.href, this.download, location.href);
		if (!t || !this.download) {
			e.call(this);
			return;
		}
		m.has(t.sessionId) || (m.add(t.sessionId), u(a(t), { method: "POST" }).then(async (e) => {
			let t = await e.json();
			t.result === "saved" ? p("saved") : t.result === "cancelled" ? p("cancelled") : p("error");
		}).catch(() => p("error")).finally(() => m.delete(t.sessionId)));
	};
}
var g = globalThis.Worker;
typeof g == "function" && (globalThis.Worker = new Proxy(g, { construct(e, t, n) {
	let i = Reflect.construct(e, t, n);
	return t[1]?.name === "dsh-file-upload" ? r(i, u, location.href) : i;
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
				generation: c,
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
			for (let e = 0; e < r.length || e === 0; e += l) {
				let n = r.subarray(e, e + l), i = new Uint8Array(n.length + 1);
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
globalThis.WebSocket = new Proxy(_, { construct(e, n, r) {
	return t(n[0], location.href) ? new v(n[0], n[1]) : Reflect.construct(e, n, r);
} });
//#endregion
export { e as Stream, u as workerFetch };
