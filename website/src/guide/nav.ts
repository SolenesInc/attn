import { getEntry } from 'astro:content';

export interface GuideTopic {
  topic: string;
  summary: string;
}

export interface GuideSection {
  title: string;
  topics: GuideTopic[];
}

const TOPIC_ROW = /^\|\s*\[([^\]]+)\]\([^)]+\)\s*\|\s*(.*?)\s*\|\s*$/;

function parseSections(readme: string): GuideSection[] {
  const sections: GuideSection[] = [];
  for (const line of readme.split('\n')) {
    if (line.startsWith('## ')) {
      sections.push({ title: line.slice(3).trim(), topics: [] });
      continue;
    }
    const row = line.match(TOPIC_ROW);
    if (row) sections.at(-1)?.topics.push({ topic: row[1], summary: row[2] });
  }
  return sections.filter((section) => section.topics.length > 0);
}

export async function guideSections(): Promise<GuideSection[]> {
  const readme = await getEntry('guide', 'README');
  return parseSections(readme?.body ?? '');
}

export function neighbors(sections: GuideSection[], topic: string | null) {
  const order = sections.flatMap((section) => section.topics);
  const index = topic === null ? -1 : order.findIndex((entry) => entry.topic === topic);
  return { previous: order[index - 1], next: order[index + 1] };
}
