import {
  GHOSTTY_OUT_OF_SPACE,
  GHOSTTY_SUCCESS,
  type GhosttyExports,
} from './abi';

const WASM32_USIZE_BYTES = 4;
const INITIAL_OUTPUT_CAPACITY = 64;

const REQUIRED_KEY_EXPORTS = [
  'ghostty_type_json',
  'ghostty_key_event_new',
  'ghostty_key_event_free',
  'ghostty_key_event_set_action',
  'ghostty_key_event_set_key',
  'ghostty_key_event_set_mods',
  'ghostty_key_event_set_consumed_mods',
  'ghostty_key_event_set_composing',
  'ghostty_key_event_set_utf8',
  'ghostty_key_event_set_unshifted_codepoint',
  'ghostty_key_encoder_new',
  'ghostty_key_encoder_free',
  'ghostty_key_encoder_setopt',
  'ghostty_key_encoder_setopt_from_terminal',
  'ghostty_key_encoder_encode',
] as const satisfies readonly (keyof GhosttyExports)[];

const REQUIRED_ENUM_VALUES = {
  GhosttyKey: ['UNIDENTIFIED', 'ARROW_DOWN', 'ARROW_LEFT', 'ARROW_RIGHT', 'ARROW_UP'],
  GhosttyKeyAction: ['RELEASE', 'PRESS', 'REPEAT'],
  GhosttyKeyEncoderOption: [
    'CURSOR_KEY_APPLICATION',
    'KEYPAD_KEY_APPLICATION',
    'IGNORE_KEYPAD_WITH_NUMLOCK',
    'ALT_ESC_PREFIX',
    'MODIFY_OTHER_KEYS_STATE_2',
    'KITTY_FLAGS',
    'MACOS_OPTION_AS_ALT',
    'BACKARROW_KEY_MODE',
  ],
  GhosttyOptionAsAlt: ['FALSE', 'TRUE', 'LEFT', 'RIGHT'],
} as const;

type EnumName = keyof typeof REQUIRED_ENUM_VALUES;

interface GhosttyEnumManifest {
  kind: 'enum';
  values: Record<string, number>;
}

interface GhosttyTypeManifest {
  schema: number;
  abi: {
    pointer_size: number;
    usize_size: number;
  };
  types: Record<string, unknown>;
}

export interface GhosttyKeyAbi {
  readonly keys: Readonly<Record<string, number>>;
  readonly actions: Readonly<Record<string, number>>;
  readonly encoderOptions: Readonly<Record<string, number>>;
  readonly optionAsAlt: Readonly<Record<string, number>>;
}

export type TerminalKeyAction = 'press' | 'release' | 'repeat';

export interface TerminalKeyEvent {
  action: TerminalKeyAction;
  key: string;
  mods?: number;
  consumedMods?: number;
  composing?: boolean;
  utf8?: string;
  unshiftedCodepoint?: number;
}

function assertKeyExports(exports: GhosttyExports): void {
  for (const name of REQUIRED_KEY_EXPORTS) {
    if (typeof exports[name] !== 'function') {
      throw new Error(`libghostty-vt is missing required WASM export ${name}`);
    }
  }
}

function readNullTerminatedJson(exports: GhosttyExports): unknown {
  const ptr = exports.ghostty_type_json();
  const bytes = new Uint8Array(exports.memory.buffer);
  if (!Number.isInteger(ptr) || ptr <= 0 || ptr >= bytes.length) {
    throw new Error(`libghostty-vt returned an invalid type manifest pointer: ${ptr}`);
  }

  let end = ptr;
  while (end < bytes.length && bytes[end] !== 0) end += 1;
  if (end === bytes.length) {
    throw new Error('libghostty-vt type manifest is not null-terminated');
  }

  try {
    return JSON.parse(new TextDecoder().decode(bytes.subarray(ptr, end)));
  } catch (error) {
    const detail = error instanceof Error ? `: ${error.message}` : '';
    throw new Error(`libghostty-vt type manifest is not valid JSON${detail}`);
  }
}

function readEnum(manifest: GhosttyTypeManifest, name: EnumName): Readonly<Record<string, number>> {
  const raw = manifest.types[name] as Partial<GhosttyEnumManifest> | undefined;
  if (raw?.kind !== 'enum' || !raw.values || typeof raw.values !== 'object') {
    throw new Error(`libghostty-vt type manifest is missing enum ${name}`);
  }

  const values: Record<string, number> = {};
  for (const [key, value] of Object.entries(raw.values)) {
    if (!Number.isInteger(value)) {
      throw new Error(`libghostty-vt enum ${name}.${key} is not an integer`);
    }
    values[key] = value;
  }
  for (const key of REQUIRED_ENUM_VALUES[name]) {
    if (values[key] === undefined) {
      throw new Error(`libghostty-vt type manifest is missing ${name}.${key}`);
    }
  }
  return Object.freeze(values);
}

export function readGhosttyKeyAbi(exports: GhosttyExports): GhosttyKeyAbi {
  assertKeyExports(exports);
  const raw = readNullTerminatedJson(exports) as Partial<GhosttyTypeManifest>;
  if (raw.schema !== 1) {
    throw new Error(`Unsupported libghostty-vt type manifest schema: ${String(raw.schema)}`);
  }
  if (raw.abi?.pointer_size !== 4 || raw.abi.usize_size !== WASM32_USIZE_BYTES) {
    throw new Error(
      `Unsupported libghostty-vt WASM ABI: pointer=${String(raw.abi?.pointer_size)}, usize=${String(raw.abi?.usize_size)}`,
    );
  }
  if (!raw.types || typeof raw.types !== 'object') {
    throw new Error('libghostty-vt type manifest has no types');
  }

  const manifest = raw as GhosttyTypeManifest;
  return Object.freeze({
    keys: readEnum(manifest, 'GhosttyKey'),
    actions: readEnum(manifest, 'GhosttyKeyAction'),
    encoderOptions: readEnum(manifest, 'GhosttyKeyEncoderOption'),
    optionAsAlt: readEnum(manifest, 'GhosttyOptionAsAlt'),
  });
}

export function assertGhosttyKeyNames(abi: GhosttyKeyAbi, names: readonly string[]): void {
  for (const name of names) {
    enumValue(abi.keys, 'GhosttyKey', name);
  }
}

function enumValue(values: Readonly<Record<string, number>>, enumName: string, name: string): number {
  const value = values[name];
  if (value === undefined) {
    throw new Error(`libghostty-vt type manifest is missing ${enumName}.${name}`);
  }
  return value;
}

export class GhosttyKeyEncoder {
  private readonly textEncoder = new TextEncoder();
  private readonly textDecoder = new TextDecoder();
  private viewBuffer: ArrayBuffer;
  private dataView: DataView;

  private encoder = 0;
  private event = 0;
  private pWritten = 0;
  private outputPtr = 0;
  private outputCapacity = 0;
  private utf8Ptr = 0;
  private utf8Capacity = 0;
  private freed = false;

  constructor(
    private readonly e: GhosttyExports,
    private readonly terminal: number,
    private readonly abi: GhosttyKeyAbi,
  ) {
    this.viewBuffer = e.memory.buffer;
    this.dataView = new DataView(this.viewBuffer);
    let out = 0;
    try {
      out = e.ghostty_wasm_alloc_opaque();
      if (!out) throw new Error('ghostty_wasm_alloc_opaque failed');

      const encoderResult = e.ghostty_key_encoder_new(0, out);
      if (encoderResult !== GHOSTTY_SUCCESS) {
        throw new Error(`ghostty_key_encoder_new failed: ${encoderResult}`);
      }
      this.encoder = this.view().getUint32(out, true);

      const eventResult = e.ghostty_key_event_new(0, out);
      if (eventResult !== GHOSTTY_SUCCESS) {
        throw new Error(`ghostty_key_event_new failed: ${eventResult}`);
      }
      this.event = this.view().getUint32(out, true);

      this.pWritten = this.allocate(WASM32_USIZE_BYTES);
      this.ensureOutputCapacity(INITIAL_OUTPUT_CAPACITY);
    } catch (error) {
      this.releaseAllocatedResources();
      throw error;
    } finally {
      if (out) e.ghostty_wasm_free_opaque(out);
    }
  }

  encode(input: TerminalKeyEvent): string {
    if (this.freed) throw new Error('GhosttyKeyEncoder is freed');

    const action = enumValue(this.abi.actions, 'GhosttyKeyAction', input.action.toUpperCase());
    const key = enumValue(this.abi.keys, 'GhosttyKey', input.key);
    const utf8 = input.utf8 ? this.textEncoder.encode(input.utf8) : null;
    if (utf8?.length) {
      this.ensureUtf8Capacity(utf8.length);
      new Uint8Array(this.e.memory.buffer, this.utf8Ptr, utf8.length).set(utf8);
    }

    this.e.ghostty_key_event_set_action(this.event, action);
    this.e.ghostty_key_event_set_key(this.event, key);
    this.e.ghostty_key_event_set_mods(this.event, input.mods ?? 0);
    this.e.ghostty_key_event_set_consumed_mods(this.event, input.consumedMods ?? 0);
    this.e.ghostty_key_event_set_composing(this.event, input.composing ? 1 : 0);
    this.e.ghostty_key_event_set_utf8(this.event, utf8?.length ? this.utf8Ptr : 0, utf8?.length ?? 0);
    this.e.ghostty_key_event_set_unshifted_codepoint(this.event, input.unshiftedCodepoint ?? 0);

    this.e.ghostty_key_encoder_setopt_from_terminal(this.encoder, this.terminal);
    // Terminal mode sync resets attn's macOS Option-as-Alt policy.
    this.view().setInt32(this.pWritten, this.abi.optionAsAlt.TRUE, true);
    this.e.ghostty_key_encoder_setopt(
      this.encoder,
      this.abi.encoderOptions.MACOS_OPTION_AS_ALT,
      this.pWritten,
    );
    this.view().setUint32(this.pWritten, 0, true);
    let result = this.e.ghostty_key_encoder_encode(
      this.encoder,
      this.event,
      this.outputPtr,
      this.outputCapacity,
      this.pWritten,
    );

    if (result === GHOSTTY_OUT_OF_SPACE) {
      const required = this.view().getUint32(this.pWritten, true);
      if (required <= this.outputCapacity) {
        throw new Error(`ghostty_key_encoder_encode requested invalid capacity ${required}`);
      }
      this.ensureOutputCapacity(required);
      this.view().setUint32(this.pWritten, 0, true);
      result = this.e.ghostty_key_encoder_encode(
        this.encoder,
        this.event,
        this.outputPtr,
        this.outputCapacity,
        this.pWritten,
      );
    }

    if (result !== GHOSTTY_SUCCESS) {
      throw new Error(`ghostty_key_encoder_encode failed: ${result}`);
    }
    const written = this.view().getUint32(this.pWritten, true);
    if (written > this.outputCapacity) {
      throw new Error(`ghostty_key_encoder_encode wrote beyond its buffer: ${written}`);
    }
    if (written === 0) return '';
    return this.textDecoder.decode(new Uint8Array(this.e.memory.buffer, this.outputPtr, written));
  }

  free(): void {
    if (this.freed) return;
    this.freed = true;
    this.releaseAllocatedResources();
  }

  private view(): DataView {
    if (this.viewBuffer !== this.e.memory.buffer) {
      this.viewBuffer = this.e.memory.buffer;
      this.dataView = new DataView(this.viewBuffer);
    }
    return this.dataView;
  }

  private ensureOutputCapacity(required: number): void {
    if (this.outputCapacity >= required) return;
    const capacity = Math.max(required, this.outputCapacity * 2, INITIAL_OUTPUT_CAPACITY);
    const next = this.allocate(capacity);
    if (this.outputPtr) this.e.ghostty_wasm_free(this.outputPtr, this.outputCapacity);
    this.outputPtr = next;
    this.outputCapacity = capacity;
  }

  private ensureUtf8Capacity(required: number): void {
    if (this.utf8Capacity >= required) return;
    const capacity = Math.max(required, this.utf8Capacity * 2, 8);
    const next = this.allocate(capacity);
    if (this.utf8Ptr) this.e.ghostty_wasm_free(this.utf8Ptr, this.utf8Capacity);
    this.utf8Ptr = next;
    this.utf8Capacity = capacity;
  }

  private allocate(size: number): number {
    const ptr = this.e.ghostty_wasm_alloc(size);
    if (!ptr) throw new Error(`ghostty_wasm_alloc failed for ${size} bytes`);
    return ptr;
  }

  private releaseAllocatedResources(): void {
    if (this.utf8Ptr) this.e.ghostty_wasm_free(this.utf8Ptr, this.utf8Capacity);
    if (this.outputPtr) this.e.ghostty_wasm_free(this.outputPtr, this.outputCapacity);
    if (this.pWritten) this.e.ghostty_wasm_free(this.pWritten, WASM32_USIZE_BYTES);
    if (this.event) this.e.ghostty_key_event_free(this.event);
    if (this.encoder) this.e.ghostty_key_encoder_free(this.encoder);
    this.utf8Ptr = 0;
    this.utf8Capacity = 0;
    this.outputPtr = 0;
    this.outputCapacity = 0;
    this.pWritten = 0;
    this.event = 0;
    this.encoder = 0;
  }
}
