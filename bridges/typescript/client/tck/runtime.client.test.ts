/* Hand-written oracle against kitchen-sink.yaml, on the composed ts-core +
 * ts-client output: what ts-client adds — Zod schemas, fetch/JWT client with
 * 429 retry, ApiError + field-error mapping. Mocked fetch; no backend. */
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  api,
  configureApiBase,
  configureRetry,
  PAGINATION,
  resetRetry,
  setToken,
} from "./generated/src/generated/client";
import { OrderCreateSchema, zodFieldErrors } from "./generated/src/generated/schemas";

/** Awaits `p`, returning the thrown ApiError (fails the test if it resolved). */
async function captureError(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p;
  } catch (e) {
    return e as ApiError;
  }
  throw new Error("expected the promise to reject");
}

describe("model-derived constants", () => {
  it("exposes pagination defaults from project.pagination", () => {
    // kitchen-sink.yaml: default_page_size: 20, max_page_size: 200
    expect(PAGINATION.defaultPageSize).toBe(20);
    expect(PAGINATION.maxPageSize).toBe(200);
  });
});

describe("HTTP client", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    setToken(null);
    configureApiBase("");
    resetRetry();
  });

  it("attaches the bearer token and JSON content type", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 }),
    );
    vi.stubGlobal("fetch", fetchMock);
    configureApiBase("");
    setToken("jwt-token");

    await api("/foo", { method: "POST", body: JSON.stringify({ a: 1 }) });

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/foo");
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe("Bearer jwt-token");
    expect(headers.get("Content-Type")).toBe("application/json");
  });

  it("throws ApiError with status and detail on non-2xx", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ code: "CONCURRENCY_CONFLICT" }), { status: 409 }),
      ),
    );
    configureApiBase("");

    await expect(api("/foo")).rejects.toMatchObject({
      name: "ApiError",
      status: 409,
      detail: { code: "CONCURRENCY_CONFLICT" },
    });
  });

  it("flags a concurrency conflict by status + code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ code: "CONCURRENCY_CONFLICT" }), { status: 409 }),
      ),
    );
    configureApiBase("");

    const err = await captureError(api("/foo"));
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe("CONCURRENCY_CONFLICT");
    expect(err.isConflict()).toBe(true);
    expect(err.isConcurrencyConflict()).toBe(true);
  });

  it("distinguishes a non-concurrency 409 (unique violation)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ code: "UNIQUE_CONFLICT" }), { status: 409 }),
      ),
    );
    configureApiBase("");

    const err = await captureError(api("/foo"));
    expect(err.isConflict()).toBe(true);
    expect(err.isConcurrencyConflict()).toBe(false);
  });

  it("leaves `code` undefined when the server sends none", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ message: "nope" }), { status: 400 }),
      ),
    );
    configureApiBase("");

    const err = await captureError(api("/foo"));
    expect(err.code).toBeUndefined();
    expect(err.isConflict()).toBe(false);
    expect(err.isConcurrencyConflict()).toBe(false);
  });

  it("parses JSON responses", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ token: "abc" }), { status: 200 })),
    );
    configureApiBase("");

    await expect(api<{ token: string }>("/login")).resolves.toEqual({ token: "abc" });
  });

  it("retries a 429 and resolves when the server recovers", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response("", { status: 429, headers: { "Retry-After": "0" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    configureApiBase("");
    configureRetry({ maxRetries: 3, baseDelayMs: 1, maxDelayMs: 1 });

    await expect(api("/foo")).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("gives up after maxRetries and surfaces a rate-limit ApiError", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ code: "RATE_LIMITED" }), {
        status: 429,
        headers: { "Retry-After": "60" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    configureApiBase("");
    configureRetry({ maxRetries: 2, baseDelayMs: 1, maxDelayMs: 1, respectRetryAfter: false });

    const err = await captureError(api("/foo"));
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(429);
    expect(err.isRateLimited()).toBe(true);
    expect(err.retryAfterSeconds).toBe(60);
    expect(fetchMock).toHaveBeenCalledTimes(3); // initial + 2 retries
  });
});

describe("validation error mapping", () => {
  it("extracts server field errors from a ValidationProblemDetails body", () => {
    const err = new ApiError(400, {
      errors: { email: ["The Email field is required."], title: ["Too short", "Too long"] },
    });
    expect(err.isValidationError()).toBe(true);
    expect(err.fieldErrors()).toEqual({
      email: "The Email field is required.",
      title: "Too short",
    });
  });

  it("returns no field errors for non-validation responses", () => {
    const err = new ApiError(409, { code: "UNIQUE_CONFLICT" });
    expect(err.isValidationError()).toBe(false);
    expect(err.fieldErrors()).toEqual({});
  });

  it("flattens a ZodError into one message per field", () => {
    const parsed = OrderCreateSchema.safeParse({});
    expect(parsed.success).toBe(false);
    if (parsed.success) return;
    const errors = zodFieldErrors(parsed.error);
    expect(typeof errors.orderNumber).toBe("string");
    expect(typeof errors.totalAmount).toBe("string");
  });
});