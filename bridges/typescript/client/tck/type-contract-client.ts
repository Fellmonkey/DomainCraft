/* Hand-written oracle against kitchen-sink.yaml, on the composed ts-core +
 * ts-client output: the Create/Update/Patch DTO projections from `schemas.ts`
 * (read-side contract lives in ts-core's tck). Typechecked by `tsc --noEmit`;
 * not run by vitest. */
import type {
  DocumentCreate,
  DocumentPatch,
  DocumentUpdate,
  OrderPatch,
  ProductCreate,
  ProductPatch,
  ReviewCreate,
  UserCreate,
} from "./generated/src/generated/schemas";

// ---- type equality harness ----
type Equal<X, Y> =
  (<T>() => T extends X ? 1 : 2) extends
  (<T>() => T extends Y ? 1 : 2) ? true : false;
type Expect<T extends true> = T;

// ===== 3. Create / Update projections =====
// Optional create fields are `T | undefined` (may be omitted).
type _CreatePhone = Expect<Equal<UserCreate["phoneNumbers"], string[] | undefined>>;

// `hidden` is CLIENT-SETTABLE: present on create, absent from read.
type _CreateInternalNotes = Expect<Equal<ProductCreate["internalNotes"], string | undefined>>;

declare const userCreate: UserCreate;
// @ts-expect-error `password` is set through the auth flow, not the entity create DTO
userCreate.password;

declare const reviewCreate: ReviewCreate;
// @ts-expect-error `readonly` fields are server-owned, never client-settable
reviewCreate.sentiment;

// Optimistic-lock `version` exists only on update (concurrency token).
declare const documentCreate: DocumentCreate;
// @ts-expect-error `version` is a feature field, not accepted on create
documentCreate.version;
type _VersionOnUpdate = Expect<Equal<DocumentUpdate["version"], number | undefined>>;

// ===== 3b. Patch projection (merge-patch: scalar + single FK + version) =====
// The concurrency token IS echoed on patch (same optimistic-lock precondition).
type _VersionOnPatch = Expect<Equal<DocumentPatch["version"], number | undefined>>;

// Single foreign keys and hidden (client-settable) scalars are patchable.
type _PatchFolderId = Expect<Equal<DocumentPatch["folderId"], string | undefined>>;
type _PatchInternalNotes = Expect<Equal<ProductPatch["internalNotes"], string | undefined>>;

declare const documentPatch: DocumentPatch;
// @ts-expect-error enums are not patchable (server's ApplyPatch rejects them)
documentPatch.status;
// @ts-expect-error json blobs are not patchable
documentPatch.metadata;

declare const orderPatch: OrderPatch;
// @ts-expect-error arrays are not patchable
orderPatch.trackingNumbers;