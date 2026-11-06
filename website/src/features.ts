import { getCollection } from 'astro:content';

export async function orderedFeatures() {
  const features = await getCollection('features');
  return features.sort((a, b) => a.data.order - b.data.order);
}
