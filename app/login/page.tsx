import type { Metadata } from "next";
import { Suspense } from "react";
import LoginForm from "./LoginForm";

export const metadata: Metadata = {
  title: "登录 · Monitored",
  description: "登录 Monitored 自部署设备遥测控制台。",
};

export default function LoginPage() {
  return (
    <Suspense
      fallback={
        <div className="auth-loading" role="status">
          <strong>正在加载登录页</strong>
        </div>
      }
    >
      <LoginForm />
    </Suspense>
  );
}
