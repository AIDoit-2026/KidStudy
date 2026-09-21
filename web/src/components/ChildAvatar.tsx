/**
 * 孩子头像：用 emoji 而不是图片资源。
 *
 * 理由：不引入图片管线、离线可用、体积为零，对 3–8 岁这个年龄段也足够可爱。
 * AVATARS 是「id → emoji」的唯一来源，选孩子页与家长设置页共用，避免两处各写一份后走偏。
 */
export const AVATARS: Record<string, string> = {
  panda: '🐼',
  rabbit: '🐰',
  tiger: '🐯',
  cat: '🐱',
  dog: '🐶',
  penguin: '🐧',
  fox: '🦊',
  bear: '🐻',
}

export const AVATAR_IDS = Object.keys(AVATARS)

export function avatarEmoji(id: string | undefined): string {
  return (id && AVATARS[id]) || '🐻'
}

export function ChildAvatar({ id, size = 'lg' }: { id?: string; size?: 'sm' | 'lg' }) {
  return (
    <span className={size === 'lg' ? 'text-5xl' : 'text-2xl'} aria-hidden="true">
      {avatarEmoji(id)}
    </span>
  )
}
