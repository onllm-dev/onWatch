import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    ignores: ["dist/**", "node_modules/**", "*.vsix", "esbuild.mjs", "eslint.config.mjs", "vitest.config.ts", "scripts/**"],
  },
  ...tseslint.configs.recommended,
  {
    files: ["src/**/*.ts", "test/**/*.ts"],
    rules: {
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_" }],
      eqeqeq: ["error", "always"],
      curly: ["error", "all"],
    },
  },
  {
    // Pure modules must stay free of the vscode API so they run under vitest.
    files: ["src/model.ts", "src/discovery.ts", "src/settings.ts", "src/notifications.ts", "src/client.ts", "src/icons.ts", "src/quickViewHtml.ts"],
    rules: {
      "no-restricted-imports": ["error", { paths: ["vscode"] }],
    },
  },
);
