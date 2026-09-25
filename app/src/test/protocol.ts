import type { Convert } from '../types/generated';

type ProtocolType = {
  [K in keyof typeof Convert]: K extends `to${string}` ? ReturnType<(typeof Convert)[K]> : never;
}[keyof typeof Convert];

type Wire<T> = T extends Date
  ? string
  : T extends string
  ? `${T}`
  : T extends readonly (infer U)[]
    ? Wire<U>[]
    : T extends object
      ? { [K in keyof T as string extends K ? never : number extends K ? never : K]: Wire<T[K]> }
      : T;

type NameIn<Field extends string, T> = T extends { [F in Field]: infer N extends string }
  ? string extends N ? never : `${N}`
  : never;

type Named<Field extends string, Name extends string> = ProtocolType extends infer T
  ? T extends unknown
    ? Name extends NameIn<Field, T> ? T : never
    : never
  : never;

export type CommandName = NameIn<'cmd', ProtocolType>;
type EventName = NameIn<'event', ProtocolType>;

export type CommandMessage<C extends CommandName = CommandName> = {
  [N in C]: Wire<Named<'cmd', N>>;
}[C];

export type EventMessage<E extends EventName = EventName> = {
  [N in E]: Wire<Named<'event', N>>;
}[E];
