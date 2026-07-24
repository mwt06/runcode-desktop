// ESLint 只为一件事存在:替我们盯住 TypeScript 看不见的 React 规则——尤其是
// hook 的依赖数组。tsc 能证明类型对,证明不了 useEffect 少列了一个依赖。
import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import reactHooks from 'eslint-plugin-react-hooks'

export default tseslint.config(
  // wailsjs/ 与 core/protocol/ 都是生成物:前者由 wails,后者由 `go run ./tools/protogen`。
  // 它们的正确性由 protogen --check 的漂移门禁保证,手改会被下次生成覆盖。
  { ignores: ['dist/**', 'wailsjs/**', 'node_modules/**', 'src/core/protocol/**'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['src/**/*.{ts,tsx}'],
    plugins: { 'react-hooks': reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // 依赖数组漏项是本项目最可能出现的真 bug(状态钩子是手写的),按错误处理。
      'react-hooks/exhaustive-deps': 'error',
      // 生成的协议层与 bridge 里有大量 any 交互,交给 tsc 把关即可。
      '@typescript-eslint/no-explicit-any': 'off',
      // 空 catch 是本项目刻意的"失败即忽略"写法,已逐处写了注释。
      'no-empty': ['error', { allowEmptyCatch: true }],
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    },
  },
)
