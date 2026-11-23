/* Hand-written oracle against kitchen-sink.yaml: asserts at compile time that
 * ts-core's generated types + query honor the contract (wire enums, hidden/
 * password exclusions, per-type filter operators, include projections).
 * Typechecked by `tsc --noEmit`; not run by vitest. The client-layer DTO
 * projections (Create/Update/Patch) live in ts-client's tck. */
import type {
  Counter,
  Document,
  DocumentStatus,
  Order,
  OrderDetail,
  OrderItem,
  OrderListItem,
  OrderStatus,
  OrderWith,
  Payment,
  PaymentMethod,
  Product,
  ProductListItem,
  ProductStatus,
  Review,
  Tag,
  User,
  UserRole,
} from "./generated/src/generated/types";
import type { OrderFilter, OrderInclude, OrderQuery, UserFilter } from "./generated/src/generated/query";

// ---- type equality harness ----
type Equal<X, Y> =
  (<T>() => T extends X ? 1 : 2) extends
  (<T>() => T extends Y ? 1 : 2) ? true : false;
type Expect<T extends true> = T;

// ===== 1. Enums serialize as snake_case WIRE values, not declared names =====
type _OrderStatus = Expect<Equal<OrderStatus, "pending" | "processing" | "shipped" | "delivered" | "cancelled">>;
type _DocumentStatus = Expect<Equal<DocumentStatus, "draft" | "review" | "published" | "archived">>;
type _PaymentMethod = Expect<Equal<PaymentMethod, "credit_card" | "debit_card" | "pay_pal" | "bank_transfer" | "crypto">>;
type _ProductStatus = Expect<Equal<ProductStatus, "draft" | "published" | "archived">>;
type _UserRole = Expect<Equal<UserRole, "admin" | "manager" | "editor" | "user" | "viewer">>;

// ===== 2. Read DTO: hidden / sensitive fields are ABSENT =====
declare const user: User;
// @ts-expect-error `password` is sensitive — never serialized into a response DTO
user.password;

declare const product: Product;
// @ts-expect-error `internalNotes` is hidden — never serialized into a response DTO
product.internalNotes;

// `readonly` fields ARE readable (server-owned, but returned).
type _SentimentReadable = Expect<Equal<Review["sentiment"], string | null>>;

// Nullable fields are `T | null` (not `T | undefined`).
type _Bio = Expect<Equal<User["bio"], string | null>>;
type _PhoneNumbers = Expect<Equal<User["phoneNumbers"], string[] | null>>;
type _ShippingJson = Expect<Equal<Order["shippingAddress"], unknown | null>>;

// Required scalars stay plain.
type _TotalAmount = Expect<Equal<Order["totalAmount"], number>>;

// to-one relation -> foreign-key id (uuid), to-many -> target array.
type _UserId = Expect<Equal<Order["userId"], string>>;
type _Items = Expect<Equal<Order["items"], OrderItem[]>>;

// int primary key -> number (drives keyset pagination).
type _CounterId = Expect<Equal<Counter["id"], number>>;

// enum field + enum array.
type _RoleField = Expect<Equal<User["role"], UserRole>>;
type _PaymentArray = Expect<Equal<Product["supportedPaymentMethods"], PaymentMethod[] | null>>;

// ===== 4. Typed query DSL: operators restricted per field type =====
const _okNumber: OrderFilter = { totalAmount: { gte: 100, lt: 500 } };
const _okIn: OrderFilter = { status: { in: ["pending", "shipped"] } };
const _okRelationHop: OrderFilter = { "user.firstName": { contains: "Ana" } };
// `shippingAddress` is a plain `json` column: text-semantics operators only.
const _okJsonRoot: OrderFilter = { json: { "shippingAddress.city": { eq: "Berlin" } } };
// `metadata` is a `jsonb` column: ordered (numeric) operators are allowed.
const _okJsonbOrdered: UserFilter = { jsonb: { "metadata.score": { gte: 10 } } };
const _okSort: OrderQuery["sort"] = ["-totalAmount", "orderNumber"];

// @ts-expect-error numeric fields have no `contains`
const _badContains: OrderFilter = { totalAmount: { contains: "x" } };
// @ts-expect-error numeric fields have no `startsWith`
const _badStartsWith: OrderFilter = { totalAmount: { startsWith: "x" } };
// @ts-expect-error enums have no ordered (numeric) operators
const _badEnumOrdered: OrderFilter = { status: { gte: 1 } };
// @ts-expect-error ordered operators are invalid on a plain `json` root (jsonb only)
const _badJsonOrdered: OrderFilter = { json: { "shippingAddress.zip": { gte: 1000 } } };
// @ts-expect-error unknown filter field is a compile error
const _badField: OrderFilter = { notAField: "x" };
// @ts-expect-error unknown sort key is a compile error
const _badSort: OrderQuery["sort"] = ["-notAField"];

// ===== 5. include / eager-load projections =====
// List rows: [many] relations are OPTIONAL (the API omits them unless `include`d).
type _OrderItemsOptional = Expect<Equal<OrderListItem["items"], OrderItem[] | undefined>>;
type _ProductTagsOptional = Expect<Equal<ProductListItem["tags"], Tag[] | undefined>>;

// Detail rows: every [many] relation is REQUIRED (get-by-id eager-loads all).
type _OrderItemsRequired = Expect<Equal<Order["items"], OrderItem[]>>;
type _ProductTagsRequired = Expect<Equal<Product["tags"], Tag[]>>;

// `Order` IS the detail projection; `OrderWith<...>` narrows a list row.
type _OrderIsDetail = Expect<Equal<Order, OrderDetail>>;
type _OrderWithItems = Expect<Equal<OrderWith<"items">["items"], OrderItem[]>>;

// The second [many] relation on Order (`payments`) mirrors `items` exactly:
// optional in a list row, required in the detail row, and narrowable via
// `OrderWith<...>`.
type _OrderPaymentsOptional = Expect<Equal<OrderListItem["payments"], Payment[] | undefined>>;
type _OrderPaymentsRequired = Expect<Equal<Order["payments"], Payment[]>>;
type _OrderWithPayments = Expect<Equal<OrderWith<"payments">["payments"], Payment[]>>;
type _OrderIncludeUnion = Expect<Equal<OrderInclude, "items" | "payments">>;

declare const orderListItem: OrderListItem;
// @ts-expect-error a bare list row omits [many] relations unless `include`d
orderListItem["items"].length;
// @ts-expect-error a bare list row omits [many] relations unless `include`d
orderListItem["payments"].length;