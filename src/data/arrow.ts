// The part of apache-arrow the window uses: the IPC reader and the type tests. query.ts loads this module by import(),
// so that Vite puts it in a chunk of its own, out of the window's first bundle; the named imports keep the rest of the
// library (builders, writers, JSON) out of that chunk (L59).
export { DataType, tableFromIPC } from "apache-arrow";
