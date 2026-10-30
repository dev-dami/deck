// Hand-offs and task reports read the same on the phone; the parser lives
// with the phone's code (pkg/remote/web/src/handoff.ts).
export { firstLine, foldsBody, type Handoff, type HandoffState, handoffOf, subTasks } from "@web/handoff";
