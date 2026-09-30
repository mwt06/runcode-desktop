// ESLint 检查 React 生命周期与目录依赖边界：类型正确不代表依赖方向正确。
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
  ...[
    { layer: 'core', allowed: ['core', 'assets'] },
    { layer: 'ui', allowed: ['ui', 'core', 'assets'] },
    { layer: 'hooks', allowed: ['hooks'] },
  ].map(({ layer, allowed }) => ({
    files: [`src/${layer}/**/*.{ts,tsx}`],
    rules: {
      'no-restricted-imports': ['error', { patterns: [
        { regex: `^@/(?!(?:${allowed.join('|')})(?:/|$))`, message: '底层模块不能反向依赖业务上层。' },
        { group: ['../**'], message: '跨目录使用 @/ 别名，避免绕过分层检查。' },
      ] }],
      'no-restricted-syntax': ['error', {
        selector: 'ImportExpression', message: '底层模块使用静态导入，使分层边界可检查。',
      }],
    },
  })),
)
