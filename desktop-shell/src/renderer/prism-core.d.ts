// prism-core is the bare tokenizer, without prism.js's DOM plugins; its
// API is the one @types/prismjs describes.
declare module "prismjs/components/prism-core" {
  const Prism: typeof import("prismjs");
  export default Prism;
}
