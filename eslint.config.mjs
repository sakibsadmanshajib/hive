import tsParser from "@typescript-eslint/parser";
import tsPlugin from "@typescript-eslint/eslint-plugin";
import nextCoreWebVitals from "eslint-config-next/core-web-vitals";

export default [
  {
    ignores: ["**/dist/**", "**/node_modules/**", "apps/web/.next/**"],
  },
  {
    files: ["**/*.ts", "**/*.tsx"],
    ignores: ["apps/web/**/*"],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: "latest",
        sourceType: "module",
      },
    },
    plugins: {
      "@typescript-eslint": tsPlugin,
    },
    rules: {
      "@typescript-eslint/no-explicit-any": "off",
      "@typescript-eslint/no-unused-vars": [
        "warn",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
        },
      ],
    },
  },
  ...nextCoreWebVitals.map((config) => ({
    ...config,
    basePath: "apps/web",
  })),
];
