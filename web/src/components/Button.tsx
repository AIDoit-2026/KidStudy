import type { ButtonHTMLAttributes, CSSProperties, ReactNode } from 'react'

type Variant = 'primary' | 'default' | 'ghost' | 'danger'

type Props = {
  variant?: Variant
  children: ReactNode
} & ButtonHTMLAttributes<HTMLButtonElement>

const VARIANT_CLASS: Record<Variant, string> = {
  primary: 'btn-primary',
  default: '',
  ghost: 'border-transparent bg-transparent',
  danger: '',
}

const VARIANT_STYLE: Record<Variant, CSSProperties | undefined> = {
  primary: undefined,
  default: undefined,
  ghost: undefined,
  danger: { backgroundColor: 'var(--c-danger)', color: '#fff', borderColor: 'transparent' },
}

/** 项目统一按钮：大尺寸、可聚焦（大屏键盘与触屏都要好点）。 */
export function Button({ variant = 'default', className = '', style, children, ...rest }: Props) {
  return (
    <button
      type="button"
      className={`btn ${VARIANT_CLASS[variant]} ${className}`.trim()}
      style={style ?? VARIANT_STYLE[variant]}
      {...rest}
    >
      {children}
    </button>
  )
}
