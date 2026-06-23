<script lang="ts">
  import { _ } from "svelte-i18n";
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
    label: $_("shared.allProjects"),
    displayLabel: $_("shared.allProjects"),
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
    value ? projects.find((p) => p.name === value)?.name ?? value : $_("shared.allProjects"),
  );
</script>

<OptionTypeahead
  {options}
  {value}
  fallbackLabel={displayValue}
  placeholder={$_("shared.projectFilterPlaceholder")}
  title={$_("shared.selectProject")}
  emptyLabel={$_("shared.noMatchingProjects")}
  {onselect}
/>
