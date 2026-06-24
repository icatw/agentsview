<script lang="ts">
  import { m, t } from "../../i18n/index.js";
  import type { ProjectInfo } from "../../api/types/core.js";
  import OptionTypeahead from "./OptionTypeahead.svelte";

  interface Props {
    projects: ProjectInfo[];
    value: string;
    onselect: (value: string) => void;
  }

  let { projects, value, onselect }: Props = $props();

  const allOption = {
    name: "",
    label: t(m.shared_all_projects),
    displayLabel: t(m.shared_all_projects),
    count: 0,
  };

  const options = $derived.by(() => {
    const items = projects.map((p) => ({
      name: p.name,
      label: `${p.name} (${p.session_count})`,
      displayLabel: p.name,
      count: p.session_count,
    }));
    return [allOption, ...items];
  });

  const displayValue = $derived(
    value ? projects.find((p) => p.name === value)?.name ?? value : t(m.shared_all_projects),
  );
</script>

<OptionTypeahead
  {options}
  {value}
  fallbackLabel={displayValue}
  placeholder={t(m.shared_project_filter_placeholder)}
  title={t(m.shared_select_project)}
  emptyLabel={t(m.shared_no_matching_projects)}
  {onselect}
/>
