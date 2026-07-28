"use client";

import {
  Activity,
  AlertTriangle,
  ArrowRight,
  KeyRound,
  LoaderCircle,
  LockKeyhole,
  Mail,
  ShieldCheck,
} from "lucide-react";
import { useSearchParams } from "next/navigation";
import { FormEvent, useEffect, useMemo, useState } from "react";

const apiBase = process.env.NEXT_PUBLIC_API_BASE?.replace(/\/$/, "") ?? "";

type APIErrorPayload = {
  error?: {
    code?: string;
    message?: string;
  };
};

function safeReturnPath(value: string | null): string {
  if (!value || !value.startsWith("/") || value.startsWith("//")) return "/";
  try {
    const parsed = new URL(value, "http://monitored.local");
    if (parsed.origin !== "http://monitored.local" || parsed.pathname === "/login") {
      return "/";
    }
    return `${parsed.pathname}${parsed.search}${parsed.hash}`;
  } catch {
    return "/";
  }
}

export default function LoginForm() {
  const searchParams = useSearchParams();
  const returnTo = useMemo(
    () => safeReturnPath(searchParams.get("next")),
    [searchParams],
  );
  const [checking, setChecking] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [totpCode, setTOTPCode] = useState("");
  const [totpRequired, setTOTPRequired] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    const checkSession = async () => {
      if (!apiBase) {
        if (active) {
          setError("Web 未配置 Master API 地址。");
          setChecking(false);
        }
        return;
      }
      try {
        const response = await fetch(`${apiBase}/auth/me`, {
          credentials: "include",
          cache: "no-store",
        });
        if (response.ok) {
          window.location.replace(returnTo);
          return;
        }
      } catch {
        if (active) setError("无法连接 Monitored Master，请检查网络后重试。");
      }
      if (active) setChecking(false);
    };
    void checkSession();
    return () => {
      active = false;
    };
  }, [returnTo]);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!apiBase || submitting) return;
    setSubmitting(true);
    setError("");
    try {
      const response = await fetch(`${apiBase}/auth/login`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          email: email.trim(),
          password,
          totpCode: totpCode.trim(),
        }),
      });
      const payload = (await response.json().catch(() => ({}))) as APIErrorPayload;
      if (response.ok) {
        window.location.replace(returnTo);
        return;
      }
      const code = payload.error?.code;
      if (code === "totp_required") {
        setTOTPRequired(true);
        setError("此账号已启用双因素认证，请输入六位验证码。");
      } else if (code === "invalid_credentials") {
        setError("邮箱或密码不正确。");
      } else {
        setError(payload.error?.message ?? `登录失败（HTTP ${response.status}）。`);
      }
    } catch {
      setError("无法连接 Monitored Master，请检查网络后重试。");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <main className="login-shell">
      <section className="login-brand-panel" aria-label="Monitored">
        <div className="login-brand-lockup">
          <div className="brand-mark" aria-hidden="true">
            <Activity size={22} strokeWidth={2.4} />
          </div>
          <div className="brand-copy">
            <strong>Monitored</strong>
            <span>DEVICE INTELLIGENCE</span>
          </div>
        </div>
        <div className="login-brand-copy">
          <span className="eyebrow">SELF-HOSTED · LIVE MODE</span>
          <h1>设备遥测，保持在你的边界内。</h1>
          <p>
            登录后查看主机、存储、GPU、网络与基础设施的实时状态。未认证会话不会加载任何控制台数据。
          </p>
        </div>
        <div className="login-trust">
          <ShieldCheck size={18} />
          <div>
            <strong>Master 会话保护</strong>
            <span>HttpOnly Cookie · SameSite=Strict · 12 小时有效</span>
          </div>
        </div>
      </section>

      <section className="login-form-panel">
        <div className="login-card">
          <div className="login-card-heading">
            <div className="login-card-icon">
              <LockKeyhole size={21} />
            </div>
            <div>
              <span>MONITORED MASTER</span>
              <h2>登录控制台</h2>
              <p>使用部署时配置的管理员账号，或 Master 中已创建的用户。</p>
            </div>
          </div>

          {checking ? (
            <div className="login-checking" role="status">
              <LoaderCircle className="spin" size={21} />
              <strong>正在检查会话</strong>
              <span>已有有效会话时将自动进入控制台。</span>
            </div>
          ) : (
            <form className="login-form" onSubmit={submit}>
              <label className="login-field">
                <span>邮箱</span>
                <div>
                  <Mail size={17} />
                  <input
                    type="email"
                    autoComplete="username"
                    autoFocus
                    required
                    value={email}
                    onChange={(event) => setEmail(event.target.value)}
                    placeholder="admin@example.com"
                  />
                </div>
              </label>

              <label className="login-field">
                <span>密码</span>
                <div>
                  <KeyRound size={17} />
                  <input
                    type="password"
                    autoComplete="current-password"
                    required
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    placeholder="输入密码"
                  />
                </div>
              </label>

              <label className="login-field">
                <span>
                  双因素验证码
                  <small>{totpRequired ? "必填" : "启用后填写"}</small>
                </span>
                <div>
                  <ShieldCheck size={17} />
                  <input
                    type="text"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    pattern="[0-9]{6}"
                    required={totpRequired}
                    maxLength={6}
                    value={totpCode}
                    onChange={(event) =>
                      setTOTPCode(event.target.value.replace(/\D/g, "").slice(0, 6))
                    }
                    placeholder="000000"
                  />
                </div>
              </label>

              {error && (
                <div className="login-error" role="alert">
                  <AlertTriangle size={17} />
                  <span>{error}</span>
                </div>
              )}

              <button className="login-submit" type="submit" disabled={submitting}>
                {submitting ? (
                  <>
                    <LoaderCircle className="spin" size={17} /> 正在登录
                  </>
                ) : (
                  <>
                    登录 <ArrowRight size={17} />
                  </>
                )}
              </button>
            </form>
          )}
        </div>
      </section>
    </main>
  );
}
