/* Live-backend interop: runs the generated ts-client against a k6-certified
 * backend, proving the wire format is accepted (filters/sort/include round-
 * trips, enum wire values, auth, error mapping). Skipped unless API_URL is
 * set; `npm run certify` stays backend-free. */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import {
  ApiError,
  api,
  configureApiBase,
  setToken,
} from "./generated/src/generated/client";
import {
  createDocument,
  createOrder,
  createOrderItem,
  createPayment,
  createProduct,
  deleteProduct,
  getProduct,
  listOrders,
  listProducts,
  patchDocument,
  patchProduct,
} from "./generated/src/generated/api";
import type { User } from "./generated/src/generated/types";

const API_URL = process.env.API_URL ? process.env.API_URL.replace(/\/+$/, "") : "";
const ENABLED = API_URL.length > 0;

// The reference backend bootstraps its first admin through /setup with these
// fixed credentials (see compliance-suite/suite.js 00-bootstrap-users).
const ADMIN_EMAIL = "admin@test.com";
const ADMIN_PASSWORD = "Secret123!";

// Unique-per-run token keeps unique columns (sku, slug, email) re-run tolerant.
const RUN = Math.floor(Date.now() / 1000).toString(36).toUpperCase().slice(-6);

async function captureError(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p;
  } catch (e) {
    return e as ApiError;
  }
  throw new Error("expected the promise to reject");
}

describe.skipIf(!ENABLED)("interop: ts-client ↔ certified backend", () => {
  let adminId = "";
  let adminToken = "";

  beforeAll(async () => {
    // Wait briefly for the backend to become healthy (CI may start it in the
    // same step; a standalone run points at an already-running compose).
    let ready = false;
    for (let i = 0; i < 30 && !ready; i++) {
      try {
        const res = await fetch(`${API_URL}/health/ready`);
        ready = res.ok;
      } catch {
        // backend not listening yet
      }
      if (!ready) await new Promise((resolve) => setTimeout(resolve, 1000));
    }
    if (!ready) throw new Error(`backend not healthy at ${API_URL}/health/ready`);

    configureApiBase(API_URL);

    // Bootstrap the first admin (201 on a fresh DB, 409 once already set up).
    await api("api/user/setup", {
      method: "POST",
      body: JSON.stringify({
        email: ADMIN_EMAIL,
        password: ADMIN_PASSWORD,
        firstName: "Admin",
        lastName: "User",
      }),
    }).catch((e: ApiError) => {
      if (!e.isConflict()) throw e;
    });

    const login = await api<{ token: string }>("api/user/login", {
      method: "POST",
      body: JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }),
    });
    adminToken = login.token;
    setToken(adminToken);

    const me = await api<User>("api/user/me");
    adminId = me.id;
  });

  afterAll(() => {
    setToken(null);
    configureApiBase("");
  });

  it("authenticates and reads the current user", async () => {
    const me = await api<User>("api/user/me");
    expect(me.email).toBe(ADMIN_EMAIL);
    expect(me.id).toBe(adminId);
  });

  it("round-trips product CRUD through the typed client", async () => {
    const sku = `TCK-${RUN}-CRUD`;
    const created = await createProduct({
      sku,
      title: "Interop Product",
      price: 9.99,
      stock: 10,
      status: "draft",
      viewCount: 0,
    });
    expect(created.sku).toBe(sku);
    expect(created.price).toBe(9.99);

    const fetched = await getProduct(created.id);
    expect(fetched.title).toBe("Interop Product");

    // Merge-patch updates only the provided fields (price is preserved).
    const patched = await patchProduct(created.id, { title: "Interop Product (patched)" });
    expect(patched.title).toBe("Interop Product (patched)");
    expect(patched.price).toBe(9.99);

    await deleteProduct(created.id);

    await expect(getProduct(created.id)).rejects.toMatchObject({ status: 404 });
  });

  it("serializes filter/sort so the server returns the right rows", async () => {
    const prefix = `FLT-${RUN}`;
    const skuA = `${prefix}-A`;
    const skuB = `${prefix}-B`;

    await createProduct({ sku: skuA, title: "Filter Product A", price: 5, stock: 5, status: "draft", viewCount: 0 });
    await createProduct({ sku: skuB, title: "Filter Product B", price: 10, stock: 50, status: "draft", viewCount: 0 });

    // startsWith + numeric gte are serialized as `path:op:value` and ANDed server-side.
    const filtered = await listProducts({
      filter: { sku: { startsWith: prefix }, stock: { gte: 50 } },
    });
    const filteredSkus = filtered.items.map((i) => i.sku);
    expect(filteredSkus).toContain(skuB);
    expect(filteredSkus).not.toContain(skuA);

    // `in` is serialized as a parenthesized list and matches both rows.
    const inRes = await listProducts({ filter: { sku: { in: [skuA, skuB] } } });
    expect(inRes.items.map((i) => i.sku).sort()).toEqual([skuA, skuB].sort());

    // sort=-price orders B (10) before A (5) within the prefixed scope.
    const sorted = await listProducts({
      filter: { sku: { startsWith: prefix } },
      sort: ["-price"],
    });
    expect(sorted.items.map((i) => i.sku)).toEqual([skuB, skuA]);
  });

  it("expands only the `include`d [many] relation (restrict, not add)", async () => {
    // Order carries TWO reconciled [many] collections: `items` (via
    // OrderItem.orderId) and `payments` (via Payment.orderId). `include` is a
    // restrict-list — naming one relation expands only that collection, so a
    // UI table can request exactly the columns it shows. We prove it with the
    // populated `items` collection: `include=payments` must RESTRICT `items`
    // away even though the rows exist. (Product.tags is an implicit M:N with
    // no write endpoint, so it is not exercised here.)
    const product = await createProduct({
      sku: `TCK-${RUN}-ITEM`,
      title: "Include Product",
      price: 1,
      stock: 1,
      status: "draft",
      viewCount: 0,
    });

    // Orders are User-scoped; drive one through a freshly registered user.
    const email = `tck-${RUN.toLowerCase()}@test.com`;
    await api("api/user/register", {
      method: "POST",
      body: JSON.stringify({ email, password: ADMIN_PASSWORD }),
    });
    const login = await api<{ token: string }>("api/user/login", {
      method: "POST",
      body: JSON.stringify({ email, password: ADMIN_PASSWORD }),
    });
    setToken(login.token);
    const me = await api<User>("api/user/me");

    const orderNumber = `ORD-${RUN}-INC`;
    const order = await createOrder({
      orderNumber,
      userId: me.id,
      status: "pending",
      totalAmount: 10,
      subtotal: 10,
    });
    await createOrderItem({
      orderId: order.id,
      productId: product.id,
      quantity: 3,
      unitPrice: 10,
      price: 10,
    });

    // Restore the admin token for a stable read (admin bypasses @Owner).
    setToken(adminToken);

    // `include=items` expands the requested collection.
    const withItems = await listOrders({ filter: { orderNumber }, include: ["items"] });
    expect(withItems.items).toHaveLength(1);
    expect(withItems.items[0].items.map((i) => i.quantity)).toContain(3);

    // `include=payments` (the OTHER collection) RESTRICTS `items` away — the
    // core UI-TCK behavior: a table requests exactly the columns it shows.
    const withPayments = await listOrders({ filter: { orderNumber }, include: ["payments"] });
    expect(withPayments.items).toHaveLength(1);
    expect(withPayments.items[0].items ?? []).toHaveLength(0);

    // No `include` = the server's "load everything" default.
    const noInclude = await listOrders({ filter: { orderNumber } });
    expect(noInclude.items).toHaveLength(1);
    expect((noInclude.items[0].items ?? []).map((i) => i.quantity)).toContain(3);

    // Both named → both requested.
    const withBoth = await listOrders({ filter: { orderNumber }, include: ["items", "payments"] });
    expect(withBoth.items).toHaveLength(1);
    expect(withBoth.items[0].items.map((i) => i.quantity)).toContain(3);
  });

  it("round-trips enum wire values (snake_case) through create + read", async () => {
    // Order.create is User-only, Payment.create is Admin/Manager — so drive an
    // order through a fresh user, then create the payment as admin.
    const email = `pay-${RUN.toLowerCase()}@test.com`;
    await api("api/user/register", {
      method: "POST",
      body: JSON.stringify({ email, password: ADMIN_PASSWORD }),
    }).catch((e: ApiError) => {
      if (!e.isConflict()) throw e;
    });
    const login = await api<{ token: string }>("api/user/login", {
      method: "POST",
      body: JSON.stringify({ email, password: ADMIN_PASSWORD }),
    });
    setToken(login.token);
    const me = await api<User>("api/user/me");

    const order = await createOrder({
      orderNumber: `ORD-${RUN}-PAY`,
      userId: me.id,
      status: "pending",
      totalAmount: 10,
      subtotal: 10,
    });

    // Restore the admin token (Payment.create is admin/manager-scoped).
    setToken(adminToken);

    // The core-owned snake_case wire value must be accepted AND echoed back —
    // the C# bridge serializes [EnumMember] (JsonStringEnumConverter would leak
    // the PascalCase member name and reject "credit_card").
    const payment = await createPayment({
      orderId: order.id,
      amount: 25.5,
      method: "credit_card",
      status: "pending",
      attemptCount: 1,
    });
    expect(payment.method).toBe("credit_card");
  });

  it("maps a unique violation to isConflict()", async () => {
    const sku = `TCK-${RUN}-UNIQ`;
    await createProduct({ sku, title: "Unique Product", price: 1, stock: 1, status: "draft", viewCount: 0 });

    const err = await captureError(
      createProduct({ sku, title: "Duplicate SKU", price: 1, stock: 1, status: "draft", viewCount: 0 }),
    );
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(409);
    expect(err.isConflict()).toBe(true);
    expect(err.code).toBe("UNIQUE_CONFLICT");
  });

  it("maps model validation to isValidationError() + fieldErrors()", async () => {
    // sku fails the regex, title fails min length, price fails the lower bound.
    const err = await captureError(
      createProduct({ sku: "bad", title: "x", price: -1, stock: 0, status: "draft", viewCount: 0 }),
    );
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(400);
    expect(err.isValidationError()).toBe(true);
    expect(Object.keys(err.fieldErrors()).length).toBeGreaterThan(0);
  });

  it("maps an optimistic-lock conflict to isConcurrencyConflict()", async () => {
    const doc = await createDocument({
      title: "Interop Lock Doc",
      content: "content",
      status: "draft",
      authorId: adminId,
      isPublished: false,
      fileSize: 0,
    });
    const v1 = doc.version;

    // First patch with the read version succeeds and bumps the stored version.
    await patchDocument(doc.id, { title: "Interop Lock Doc v2", version: v1 });

    // A second patch with the now-stale version → 409 CONCURRENCY_CONFLICT.
    const err = await captureError(patchDocument(doc.id, { title: "stale", version: v1 }));
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(409);
    expect(err.isConcurrencyConflict()).toBe(true);
  });
});
