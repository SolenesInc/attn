/// ```
/// assert_eq!(good::two(), 2);
/// ```
pub fn two() -> u32 {
    2
}

#[cfg(test)]
mod tests {
    #[test]
    fn passes() {}

    #[test]
    fn fails() {
        panic!("this one fails");
    }

    #[test]
    #[ignore = "needs a GPU"]
    fn skips() {}
}
