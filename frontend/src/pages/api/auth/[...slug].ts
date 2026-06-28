import type { APIRoute } from "astro";
import { auth } from "../../../lib/auth";

export const prerender = false;

export const ALL: APIRoute = async ({ request }) => {
  try {
    const response = await auth.handler(request);
    console.log("[auth] status:", response.status, "for", request.url);
    return response;
  } catch (e: any) {
    console.error("[auth] error:", e.message || e);
    return new Response(e.message || "Internal error", { status: 500 });
  }
};
