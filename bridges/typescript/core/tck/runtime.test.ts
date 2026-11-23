/* Hand-written oracle against kitchen-sink.yaml: tests the runtime behavior
 * ts-core owns — query serialization (spec/query.md) and the permission matrix.
 * Never imports `schemas`/`client`/`api`: those are ts-client artifacts. */
import { describe, expect, it } from "vitest";
import { buildQueryParams, encodeFilter, orderSearchableKeys } from "./generated/src/generated/query";
import { can } from "./generated/src/generated/permissions";

describe("filter serialization (spec/query.md)", () => {
  it("serializes a bare value as `eq`", () => {
    expect(encodeFilter({ status: "pending" })).toBe("status:eq:pending");
  });

  it("serializes operators per field", () => {
    expect(encodeFilter({ totalAmount: { gte: 100, lt: 500 } })).toBe(
      "totalAmount:gte:100,totalAmount:lt:500",
    );
  });

  it("serializes `in` as a parenthesized list", () => {
    expect(encodeFilter({ status: { in: ["pending", "shipped"] } })).toBe(
      "status:in:(pending,shipped)",
    );
  });

  it("ANDs multiple fields with commas", () => {
    expect(encodeFilter({ totalAmount: { gte: 100 }, status: "pending" })).toBe(
      "totalAmount:gte:100,status:eq:pending",
    );
  });

  it("serializes JSON roots through the `json` accessor", () => {
    expect(encodeFilter({ json: { "shippingAddress.city": { eq: "Berlin" } } })).toBe(
      "shippingAddress.city:eq:Berlin",
    );
  });

  it("serializes JSONB roots through the `jsonb` accessor (ordered ops allowed)", () => {
    expect(encodeFilter({ jsonb: { "metadata.score": { gte: "10" } } })).toBe(
      "metadata.score:gte:10",
    );
  });

  it("returns undefined for empty filters", () => {
    expect(encodeFilter(undefined)).toBeUndefined();
    expect(encodeFilter({})).toBeUndefined();
  });
});

describe("model-derived query constants", () => {
  it("lists searchable fields per entity from the IR", () => {
    expect(orderSearchableKeys).toContain("orderNumber");
  });
});

describe("query params", () => {
  it("builds pagination/search/sort params", () => {
    const params = buildQueryParams({
      page: 2,
      pageSize: 10,
      sort: ["-totalAmount", "orderNumber"],
      search: "widget",
    });
    expect(params.get("page")).toBe("2");
    expect(params.get("pageSize")).toBe("10");
    expect(params.get("sort")).toBe("-totalAmount,orderNumber");
    expect(params.get("search")).toBe("widget");
  });

  it("prefers `limit` over `pageSize`", () => {
    const params = buildQueryParams({ pageSize: 5, limit: 20 });
    expect(params.get("pageSize")).toBe("20");
  });

  it("serializes `include` as a comma-joined list", () => {
    const params = buildQueryParams({ include: ["items", "tags"] });
    expect(params.get("include")).toBe("items,tags");
  });
});

describe("permission matrix", () => {
  it("grants explicit roles", () => {
    expect(can(["Admin"], "Order", "delete")).toBe(true);
    expect(can(["User"], "Order", "create")).toBe(true);
  });

  it("denies missing roles", () => {
    expect(can(["Viewer"], "Order", "delete")).toBe(false);
    expect(can(["User"], "Payment", "delete")).toBe(false);
  });

  it("treats `*` as public", () => {
    expect(can([], "Category", "read")).toBe(true);
  });

  it("treats @Owner/@ tokens as server-enforced", () => {
    expect(can(["User"], "Review", "update")).toBe(true); // @Owner
    expect(can(["User"], "User", "create")).toBe(true); // self-registration "@"
  });
});