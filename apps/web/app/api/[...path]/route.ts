import { NextRequest } from "next/server";
export const runtime = "nodejs";
export const dynamic = "force-dynamic";
async function proxy(
  request: NextRequest,
  context: { params: Promise<{ path: string[] }> },
) {
  const { path } = await context.params;
  if (
    path[0] !== "v1" ||
    path.some(
      (part) => part === ".." || part.includes("/") || part.includes("\\"),
    )
  )
    return Response.json({ error: { message: "Not found" } }, { status: 404 });
  const upstream = process.env.API_INTERNAL_URL || "http://127.0.0.1:8080";
  const url = new URL("/" + path.map(encodeURIComponent).join("/"), upstream);
  url.search = request.nextUrl.search;
  const headers = new Headers();
  for (const key of [
    "cookie",
    "content-type",
    "x-csrf-token",
    "idempotency-key",
    "origin",
    "authorization",
  ]) {
    const value = request.headers.get(key);
    if (value) headers.set(key, value);
  }
  try {
    let body: Uint8Array | undefined;
    if (request.method !== "GET" && request.method !== "HEAD" && request.body) {
      const reader = request.body.getReader();
      const chunks: Uint8Array[] = [];
      let size = 0;
      while (true) {
        const result = await reader.read();
        if (result.done) break;
        size += result.value.byteLength;
        if (size > 262144) {
          await reader.cancel();
          return Response.json(
            { error: { message: "Request exceeds 256 KiB" } },
            { status: 413 },
          );
        }
        chunks.push(result.value);
      }
      body = new Uint8Array(size);
      let offset = 0;
      for (const chunk of chunks) {
        body.set(chunk, offset);
        offset += chunk.byteLength;
      }
    }
    const result = await fetch(url, {
      method: request.method,
      headers,
      body: body as BodyInit | undefined,
      cache: "no-store",
      redirect: "manual",
      signal: AbortSignal.timeout(20000),
    });
    const responseHeaders = new Headers({ "Cache-Control": "no-store" });
    for (const key of [
      "content-type",
      "set-cookie",
      "allow",
      "retry-after",
      "x-request-id",
    ]) {
      const value = result.headers.get(key);
      if (value) responseHeaders.set(key, value);
    }
    return new Response(result.body, {
      status: result.status,
      headers: responseHeaders,
    });
  } catch {
    return Response.json(
      {
        error: {
          code: "api_unavailable",
          message:
            "The API is unavailable. Check the API process and database.",
        },
      },
      { status: 503 },
    );
  }
}
export {
  proxy as GET,
  proxy as POST,
  proxy as PATCH,
  proxy as DELETE,
  proxy as PUT,
  proxy as OPTIONS,
  proxy as HEAD,
};
