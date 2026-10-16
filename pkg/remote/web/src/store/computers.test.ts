import { describe, expect, test } from "bun:test";
import { fakeComputerRecord } from "../fakes3";
import {
  addComputer,
  type ComputerRecord,
  forgetOnRevoke,
  listComputers,
  removeComputer,
  renameComputer,
} from "./computers";
import { memoryBackend, setBackendForTests } from "./db";

describe("computers store (B4)", () => {
  test("add, rename, revoke-forget, remove", async () => {
    setBackendForTests(memoryBackend());
    await addComputer(fakeComputerRecord({ cid: "cid_a", name: "Office" }));
    await addComputer(fakeComputerRecord({ cid: "cid_b", name: "Laptop" }));
    expect((await listComputers()).map((c: ComputerRecord) => c.cid)).toEqual(["cid_a", "cid_b"]);
    expect(await renameComputer("cid_a", "Office PC")).toBe(true);
    expect(await renameComputer("cid_a", "  ")).toBe(false);
    expect(await forgetOnRevoke("cid_a")).toBe(true);
    const after = await listComputers();
    const row = after.find((c: ComputerRecord) => c.cid === "cid_a");
    expect(row?.deviceId).toBeNull();
    expect(row?.relayToken).toBeNull();
    await removeComputer("cid_b");
    expect((await listComputers()).map((c: ComputerRecord) => c.cid)).toEqual(["cid_a"]);
  });
});
