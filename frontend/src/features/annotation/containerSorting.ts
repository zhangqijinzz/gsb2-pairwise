import type { AnnotationContainer } from '../../api/annotation';

export const CONTAINER_SORT_PREFIXES_CONFIG_KEY = 'annotation_container_sort_prefixes';

export function normalizeContainerSortPrefixes(value: string): string[] {
  const seen = new Set<string>();
  const prefixes: string[] = [];
  for (const part of value.split(/[,，;；\s]+/)) {
    const prefix = part.trim().toLowerCase();
    if (!prefix || seen.has(prefix)) continue;
    seen.add(prefix);
    prefixes.push(prefix);
  }
  return prefixes.length > 0 ? prefixes : ['cyc'];
}

export function sortContainersByPrefixes(
  containers: AnnotationContainer[],
  configuredPrefixes: string,
): AnnotationContainer[] {
  const prefixes = normalizeContainerSortPrefixes(configuredPrefixes);
  const groupIndex = (name: string) => {
    const lowerName = name.toLowerCase();
    const index = prefixes.findIndex((prefix) => lowerName.startsWith(prefix));
    return index === -1 ? prefixes.length : index;
  };
  const compareNames = (left: string, right: string) =>
    left.localeCompare(right, 'en', { numeric: true, sensitivity: 'base' });

  return [...containers].sort((left, right) => {
    const leftGroup = groupIndex(left.name);
    const rightGroup = groupIndex(right.name);
    if (leftGroup !== rightGroup) return leftGroup - rightGroup;
    if (leftGroup === prefixes.length) return compareNames(left.name, right.name);
    return compareNames(right.name, left.name);
  });
}
