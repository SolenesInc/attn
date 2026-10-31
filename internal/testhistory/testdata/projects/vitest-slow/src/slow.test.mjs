it('finishes first', () => {});
it('is interrupted', async () => { await new Promise(r => setTimeout(r, 30_000)); }, 60_000);
it('never starts', () => {});
