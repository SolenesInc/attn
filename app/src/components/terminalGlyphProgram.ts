// Premultiplied alpha: every renderer using these shaders must upload with
// UNPACK_PREMULTIPLY_ALPHA_WEBGL and blend ONE, ONE_MINUS_SRC_ALPHA.

export const TERMINAL_GLYPH_VERTEX_SHADER = `#version 300 es
in vec2 a_position;
in vec2 a_texcoord;
in vec4 a_color;
in float a_mode;
uniform vec2 u_resolution;
out vec2 v_texcoord;
out vec4 v_color;
flat out float v_mode;

void main() {
  vec2 clip = (a_position / u_resolution) * 2.0 - 1.0;
  gl_Position = vec4(clip * vec2(1.0, -1.0), 0.0, 1.0);
  v_texcoord = a_texcoord;
  v_color = a_color;
  v_mode = a_mode;
}
`;

export const TERMINAL_GLYPH_FRAGMENT_SHADER = `#version 300 es
precision mediump float;
uniform sampler2D u_atlas;
in vec2 v_texcoord;
in vec4 v_color;
flat in float v_mode;
out vec4 out_color;

void main() {
  vec4 texel = texture(u_atlas, v_texcoord);
  if (v_mode > 0.5) {
    out_color = texel * v_color.a;
  } else {
    float a = v_color.a * texel.a;
    out_color = vec4(v_color.rgb * a, a);
  }
}
`;

export const GLYPH_MODE_TINT = 0;
export const GLYPH_MODE_COLOR = 1;

// Requires STRAIGHT (non-premultiplied) ImageData, as getImageData returns.
export function isColorGlyphBitmap(image: ImageData): boolean {
  const data = image.data;
  for (let i = 0; i < data.length; i += 4) {
    if (data[i + 3] === 0) continue;
    const r = data[i];
    const g = data[i + 1];
    const b = data[i + 2];
    if (Math.abs(r - g) > 12 || Math.abs(g - b) > 12 || Math.abs(r - b) > 12) {
      return true;
    }
  }
  return false;
}
