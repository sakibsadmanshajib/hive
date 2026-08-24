// TypeScript 7 (tsgo) no longer accepts side-effect imports of .css files
// without an explicit module declaration (TS2882 on app/layout.tsx).
declare module "*.css";
