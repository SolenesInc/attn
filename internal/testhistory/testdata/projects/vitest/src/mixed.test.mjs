describe('outer', () => {
  it('passes', () => { expect(1).toBe(1); });
  it('fails', () => { expect(1).toBe(2); });
  it.skip('is skipped', () => {});
  it.todo('is todo');
  it.fails('fails as expected', () => { expect(1).toBe(2); });
  let tries = 0;
  it('passes on retry', { retry: 2 }, () => { tries += 1; expect(tries).toBe(2); });
});
