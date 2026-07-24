/// <reference types="vite/client" />

// VITE_BRAND 选择白标品牌(见 core/brand)。未设即默认品牌。
interface ImportMetaEnv {
  readonly VITE_BRAND?: string
}
