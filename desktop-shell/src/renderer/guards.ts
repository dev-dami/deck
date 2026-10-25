// Readers for the plain objects the proto loader makes: snake_case keys,
// int64 as numbers, defaults filled in. Anything else reads as empty.
export type Obj = Record<string, unknown>;
export const obj = (v: unknown): Obj => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Obj) : {});
export const str = (o: Obj, k: string): string => (typeof o[k] === "string" ? (o[k] as string) : "");
export const num = (o: Obj, k: string): number => (typeof o[k] === "number" && Number.isFinite(o[k]) ? (o[k] as number) : 0);
export const bool = (o: Obj, k: string): boolean => o[k] === true;
export const strs = (o: Obj, k: string): string[] => (Array.isArray(o[k]) ? (o[k] as unknown[]).filter((x): x is string => typeof x === "string") : []);
export const list = <T>(o: Obj, k: string, f: (v: Obj) => T): T[] => (Array.isArray(o[k]) ? (o[k] as unknown[]).map((v) => f(obj(v))) : []);
