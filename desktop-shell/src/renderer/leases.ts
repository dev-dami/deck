// The globs of an agent's lease summary, as the agents tree sends it: "; "
// between leases, ", " between the globs of one, and what is said of a lease
// in brackets after its globs ("a/**, b.go (exclusive); c/**"). Each glob
// carries that note ("" for none), so it can be shown on its own.
export interface LeaseGlob {
  glob: string;
  note: string;
}

export function leaseGlobs(leases: string): LeaseGlob[] {
  return leases.split(";").flatMap((group) => {
    const m = /\(([^()]*)\)\s*$/.exec(group);
    const note = m?.[1]?.trim() ?? "";
    return (m ? group.slice(0, m.index) : group)
      .split(",")
      .map((glob) => glob.trim())
      .filter((glob) => glob !== "")
      .map((glob) => ({ glob, note }));
  });
}
