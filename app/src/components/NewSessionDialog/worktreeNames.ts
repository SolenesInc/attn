const ADJECTIVES = [
  'amber', 'bashful', 'brisk', 'chaotic', 'chipper', 'cosmic', 'cranky',
  'crispy', 'dapper', 'dizzy', 'drowsy', 'feral', 'fluffy', 'frosty', 'fussy',
  'gloomy', 'grumpy', 'hasty', 'jaunty', 'jolly', 'lanky', 'lucky', 'moody',
  'nimble', 'peppy', 'plucky', 'prickly', 'quirky', 'rowdy', 'rugged', 'rustic',
  'salty', 'scruffy', 'sleepy', 'smug', 'snappy', 'sneaky', 'soggy', 'spicy',
  'sturdy', 'sulky', 'sunny', 'surly', 'tipsy', 'unruly', 'velvet', 'wary',
  'whimsy', 'wily', 'zesty',
];

const NOUNS = [
  'alpaca', 'axolotl', 'badger', 'beetle', 'bison', 'capybara', 'chinchilla',
  'dingo', 'ferret', 'gecko', 'gerbil', 'gopher', 'hedgehog', 'heron', 'ibex',
  'iguana', 'kestrel', 'lemur', 'llama', 'magpie', 'manatee', 'marmot',
  'meerkat', 'mongoose', 'moose', 'narwhal', 'newt', 'ocelot', 'opossum',
  'otter', 'pangolin', 'pelican', 'pigeon', 'platypus', 'puffin', 'quokka',
  'raccoon', 'seal', 'tapir', 'toucan', 'walrus', 'weasel', 'wombat', 'yak',
];

export function isBranchAlreadyExistsError(message: string): boolean {
  return /branch named ['"]?.+['"]? already exists/i.test(message);
}

const pick = <T,>(items: readonly T[], random: () => number): T =>
  items[Math.floor(random() * items.length) % items.length];

export function generateWorktreeName(
  taken: Iterable<string> = [],
  random: () => number = Math.random,
): string {
  const used = new Set(taken);

  for (let attempt = 0; attempt < 50; attempt++) {
    const name = `${pick(ADJECTIVES, random)}-${pick(NOUNS, random)}`;
    if (!used.has(name)) {
      return name;
    }
  }

  const base = `${pick(ADJECTIVES, random)}-${pick(NOUNS, random)}`;
  for (let suffix = 2; ; suffix++) {
    const name = `${base}-${suffix}`;
    if (!used.has(name)) {
      return name;
    }
  }
}
