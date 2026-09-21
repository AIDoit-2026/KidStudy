/**
 * 亮度遮罩：两层 fixed 覆盖，pointer-events 为 none（不挡点击）。
 * alpha 由 themeStore 算好写进 --veil-dark / --veil-light。
 */
export function BrightnessVeil() {
  return (
    <>
      <div className="veil veil-dark" aria-hidden="true" />
      <div className="veil veil-light" aria-hidden="true" />
    </>
  )
}
