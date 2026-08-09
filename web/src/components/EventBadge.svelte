<script lang="ts">
  import { labelColors, capitalize } from '../lib/utils'

  interface Props {
    label: string
    size?: 'sm' | 'md'
  }

  let { label, size = 'md' }: Props = $props()

  const colors = $derived(labelColors(label))
  const sizeClass = $derived(size === 'sm' ? 'px-1.5 py-0.5 text-[10px]' : 'px-2 py-0.5 text-xs')
</script>

<span class="badge {sizeClass} {colors.bg} {colors.text}">
  <span class="w-1.5 h-1.5 rounded-full {colors.dot} mr-1 inline-block"></span>
  {capitalize(label)}
</span>
