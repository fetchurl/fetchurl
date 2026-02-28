import pluginJs from "@eslint/js";
import globals from "globals";

/** @type {import('eslint').Linter.Config[]} */
export default [
	{
		files: ["**/*.js"],
		languageOptions: {
			sourceType: "module",
			globals: globals.node,
		},
	},
	pluginJs.configs.recommended,
];
