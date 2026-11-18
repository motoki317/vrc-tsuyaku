export function swappedLanguages(languages, flow, { Spoken, Target }, settingsError) {
  const spoken = languages.variants[Target] || Target;
  if (
    settingsError ||
    languages.spoken.some(l => l.code === Spoken && l.auto) ||
    !languages.targets.includes(Spoken) ||
    !languages.spoken.some(l => l.code === spoken && (flow === 'world' || !l.auto))
  )
    return null;
  return { Spoken: spoken, Target: Spoken };
}
