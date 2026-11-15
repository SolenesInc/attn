#[test]
fn passes_first() {}

#[test]
fn aborts() {
    std::process::abort();
}
