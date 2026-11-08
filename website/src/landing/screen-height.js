(() => {
  const touch = matchMedia('(pointer: coarse)');
  let heldForWidth = 0;

  function smallViewportHeight() {
    const probe = document.createElement('div');
    probe.style.cssText = 'position:fixed;top:0;width:0;height:100svh;visibility:hidden';
    document.body.append(probe);
    const height = probe.offsetHeight;
    probe.remove();
    return height;
  }

  function holdScreenHeightWhileToolbarsMove() {
    if (!touch.matches || innerWidth === heldForWidth) return;
    heldForWidth = innerWidth;
    document.documentElement.style.setProperty('--screen-height', `${smallViewportHeight()}px`);
  }

  holdScreenHeightWhileToolbarsMove();
  addEventListener('resize', holdScreenHeightWhileToolbarsMove, { passive: true });
})();
