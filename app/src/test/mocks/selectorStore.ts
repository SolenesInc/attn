export function selectorStoreMock<State>(readState: () => State) {
  return <Selected = State>(selector?: (state: State) => Selected) => {
    const state = readState();
    return selector ? selector(state) : state;
  };
}
