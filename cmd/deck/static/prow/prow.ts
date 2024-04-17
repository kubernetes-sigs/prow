import moment from "moment";
import {ProwJob, ProwJobList, ProwJobState, ProwJobType, Pull} from "../api/prow";
import {createAbortProwJobIcon} from "../common/abort";
import {cell, formatDuration, icon} from "../common/common";
import {createRerunProwJobIcon} from "../common/rerun";
import {getParameterByName} from "../common/urls";
import {FuzzySearch} from './fuzzy-search';
import {JobHistogram, JobSample} from './histogram';

declare const allBuilds: ProwJobList;
declare const spyglass: boolean;
declare const rerunCreatesJob: boolean;
declare const csrfToken: string;

function genShortRefKey(baseRef: string, pulls: Pull[] = []) {
  return [baseRef, ...pulls.map((p) => p.number)].filter((n) => n).join(",");
}

function genLongRefKey(baseRef: string, baseSha: string, pulls: Pull[] = []) {
  return [
    [baseRef, baseSha].filter((n) => n).join(":"),
    ...pulls.map((p) => [p.number, p.sha].filter((n) => n).join(":")),
  ]
    .filter((n) => n)
    .join(",");
}

interface RepoOptions {
  types: {[key: string]: boolean};
  repos: {[key: string]: boolean};
  jobs: {[key: string]: boolean};
  authors: {[key: string]: boolean};
  pulls: {[key: string]: boolean};
  states: {[key: string]: boolean};
  clusters: {[key: string]: boolean};
}

function optionsForRepo(repository: string): RepoOptions {
  const opts: RepoOptions = {
    authors: {},
    clusters: {},
    jobs: {},
    pulls: {},
    repos: {},
    states: {},
    types: {},
  };

  for (const build of allBuilds.items) {
    const {
      spec: {
        cluster = "",
        type = "",
        job = "",
        refs: {
          org = "", repo = "", pulls = [], base_ref = "",
        } = {},
      },
      status: {
        state = "",
      },
    } = build;

    opts.types[type] = true;
    opts.clusters[cluster] = true;
    opts.states[state] = true;


    const repoKey = `${org}/${repo}`;
    if (repoKey) {
      opts.repos[repoKey] = true;
    }
    if (!repository || repository === repoKey) {
      opts.jobs[job] = true;

      if (pulls.length) {
        for (const pull of pulls) {
          opts.authors[pull.author] = true;
          opts.pulls[pull.number] = true;
        }
      }
    }
  }

  return opts;
}

function redrawOptions(fz: FuzzySearch, opts: RepoOptions) {
  const ts = Object.keys(opts.types).sort();
  const selectedType = addOptions(ts, "type") as ProwJobType;
  const rs = Object.keys(opts.repos).filter((r) => r !== "/").sort();
  addOptions(rs, "repo");
  const js = Object.keys(opts.jobs).sort();
  const jobInput = document.getElementById("job-input") as HTMLInputElement;
  const jobList = document.getElementById("job-list") as HTMLUListElement;
  addOptionFuzzySearch(fz, js, "job", jobList, jobInput);

  if (selectedType !== "periodic" && selectedType !== "postsubmit") {
    const ps = Object.keys(opts.pulls).sort((a, b) => Number(b) - Number(a));
    addOptions(ps, "pull");
    const as = Object.keys(opts.authors).sort(
      (a, b) => a.toLowerCase().localeCompare(b.toLowerCase()));
    addOptions(as, "author");
  } else {
    addOptions([], "pull");
    addOptions([], "author");
  }
  const ss = Object.keys(opts.states).sort();
  addOptions(ss, "state");
  const cs = Object.keys(opts.clusters).sort();
  addOptions(cs, "cluster");
}

function adjustScroll(el: Element): void {
  const parent = el.parentElement;
  const parentRect = parent.getBoundingClientRect();
  const elRect = el.getBoundingClientRect();

  if (elRect.top < parentRect.top) {
    parent.scrollTop -= elRect.height;
  } else if (elRect.top + elRect.height >= parentRect.top
        + parentRect.height) {
    parent.scrollTop += elRect.height;
  }
}

function handleDownKey(): void {
  const activeSearches =
        document.getElementsByClassName("active-fuzzy-search");
  if (activeSearches !== null && activeSearches.length !== 1) {
    return;
  }
  const activeSearch = activeSearches[0];
  if (activeSearch.tagName !== "UL" ||
        activeSearch.childElementCount === 0) {
    return;
  }

  const selectedJobs = activeSearch.getElementsByClassName("job-selected");
  if (selectedJobs.length > 1) {
    return;
  }
  if (selectedJobs.length === 0) {
    // If no job selected, select the first one that visible in the list.
    const jobs = Array.from(activeSearch.children)
      .filter((elChild) => {
        const childRect = elChild.getBoundingClientRect();
        const listRect = activeSearch.getBoundingClientRect();
        return childRect.top >= listRect.top &&
                    (childRect.top < listRect.top + listRect.height);
      });
    if (jobs.length === 0) {
      return;
    }
    jobs[0].classList.add("job-selected");
    return;
  }
  const selectedJob = selectedJobs[0] ;
  const nextSibling = selectedJob.nextElementSibling;
  if (!nextSibling) {
    return;
  }

  selectedJob.classList.remove("job-selected");
  nextSibling.classList.add("job-selected");
  adjustScroll(nextSibling);
}

function handleUpKey(): void {
  const activeSearches =
        document.getElementsByClassName("active-fuzzy-search");
  if (activeSearches && activeSearches.length !== 1) {
    return;
  }
  const activeSearch = activeSearches[0];
  if (activeSearch.tagName !== "UL" ||
        activeSearch.childElementCount === 0) {
    return;
  }

  const selectedJobs = activeSearch.getElementsByClassName("job-selected");
  if (selectedJobs.length !== 1) {
    return;
  }

  const selectedJob = selectedJobs[0] ;
  const previousSibling = selectedJob.previousElementSibling;
  if (!previousSibling) {
    return;
  }

  selectedJob.classList.remove("job-selected");
  previousSibling.classList.add("job-selected");
  adjustScroll(previousSibling);
}

window.onload = (): void => {
  const topNavigator = document.getElementById("top-navigator")!;
  let navigatorTimeOut: any;
  const main = document.querySelector("main")! ;
  main.onscroll = () => {
    topNavigator.classList.add("hidden");
    if (navigatorTimeOut) {
      clearTimeout(navigatorTimeOut);
    }
    navigatorTimeOut = setTimeout(() => {
      if (main.scrollTop === 0) {
        topNavigator.classList.add("hidden");
      } else if (main.scrollTop > 100) {
        topNavigator.classList.remove("hidden");
      }
    }, 100);
  };
  topNavigator.onclick = () => {
    main.scrollTop = 0;
  };

  document.addEventListener("keydown", (event) => {
    if (event.keyCode === 40) {
      handleDownKey();
    } else if (event.keyCode === 38) {
      handleUpKey();
    }
  });
  // Register selection on change functions
  const filterBox = document.getElementById("filter-box")!;
  const options = filterBox.querySelectorAll("select")!;
  options.forEach((opt) => {
    opt.onchange = () => {
      redraw(fz);
    };
  });
  // Attach job status bar on click
  const stateFilter = document.getElementById("state")! as HTMLSelectElement;
  document.querySelectorAll(".job-bar-state").forEach((jb) => {
    const state = jb.id.slice("job-bar-".length);
    if (state === "unknown") {
      return;
    }
    jb.addEventListener("click", () => {
      stateFilter.value = state;
      stateFilter.onchange.call(stateFilter, new Event("change"));
    });
  });
  // Attach job histogram on click to scroll the selected build into view
  const jobHistogram = document.getElementById("job-histogram-content") as HTMLTableSectionElement;
  jobHistogram.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target == null) {
      return;
    }
    if (!target.classList.contains('active')) {
      return;
    }
    const row = target.dataset.sampleRow;
    if (row == null || row.length === 0) {
      return;
    }
    const rowNumber = Number(row);
    const builds = document.getElementById("builds")!.getElementsByTagName("tbody")[0];
    if (builds == null || rowNumber >= builds.childNodes.length) {
      return;
    }
    const targetRow = builds.childNodes[rowNumber] as HTMLTableRowElement;
    targetRow.scrollIntoView();
  });
  window.addEventListener("popstate", () => {
    const optsPopped = optionsForRepo("");
    const fzPopped = initFuzzySearch(
      "job",
      "job-input",
      "job-list",
      Object.keys(optsPopped.jobs).sort());
    redrawOptions(fzPopped, optsPopped);
    redraw(fzPopped, false);
  });
  // set dropdown based on options from query string
  const opts = optionsForRepo("");
  const fz = initFuzzySearch(
    "job",
    "job-input",
    "job-list",
    Object.keys(opts.jobs).sort());
  redrawOptions(fz, opts);
  redraw(fz);
};

function displayFuzzySearchResult(el: HTMLElement, inputContainer: ClientRect | DOMRect): void {
  el.classList.add("active-fuzzy-search");
  el.style.top = `${inputContainer.height - 1  }px`;
  el.style.width = `${inputContainer.width  }px`;
  el.style.height = `${200  }px`;
  el.style.zIndex = "9999";
}

function fuzzySearch(fz: FuzzySearch, id: string, list: HTMLElement, input: HTMLInputElement): void {
  const inputValue = input.value.trim();
  addOptionFuzzySearch(fz, fz.search(inputValue), id, list, input, true);
}

function validToken(token: number): boolean {
  // 0-9
  if (token >= 48 && token <= 57) {
    return true;
  }
  // a-z
  if (token >= 65 && token <= 90) {
    return true;
  }
  // - and backspace
  return token === 189 || token === 8;
}

function handleEnterKeyDown(fz: FuzzySearch, list: HTMLElement, input: HTMLInputElement): void {
  const selectedJobs = list.getElementsByClassName("job-selected");
  if (selectedJobs && selectedJobs.length === 1) {
    input.value = (selectedJobs[0] as HTMLElement).innerHTML;
  }
  // TODO(@qhuynh96): according to discussion in https://github.com/kubernetes/test-infra/pull/7165, the
  // fuzzy search should respect user input no matter it is in the list or not. User may
  // experience being redirected back to default view if the search input is invalid.
  input.blur();
  list.classList.remove("active-fuzzy-search");
  redraw(fz);
}

function registerFuzzySearchHandler(fz: FuzzySearch, id: string, list: HTMLElement, input: HTMLInputElement): void {
  input.addEventListener("keydown", (event) => {
    if (event.keyCode === 13) {
      handleEnterKeyDown(fz, list, input);
    } else if (validToken(event.keyCode)) {
      // Delay 1 frame that the input character is recorded before getting
      // input value
      setTimeout(() => fuzzySearch(fz, id, list, input), 32);
    }
  });
}

function initFuzzySearch(id: string, inputId: string, listId: string,
  data: string[]): FuzzySearch {
  const fz = new FuzzySearch(data);
  const el = document.getElementById(id)!;
  const input = document.getElementById(inputId)! as HTMLInputElement;
  const list = document.getElementById(listId)!;

  list.classList.remove("active-fuzzy-search");
  input.addEventListener("focus", () => {
    fuzzySearch(fz, id, list, input);
    displayFuzzySearchResult(list, el.getBoundingClientRect());
  });
  input.addEventListener("blur", () => list.classList.remove("active-fuzzy-search"));

  registerFuzzySearchHandler(fz, id, list, input);
  return fz;
}

function registerJobResultEventHandler(fz: FuzzySearch, li: HTMLElement, input: HTMLInputElement) {
  li.addEventListener("mousedown", (event) => {
    input.value = (event.currentTarget as HTMLElement).innerHTML;
    redraw(fz);
  });
  li.addEventListener("mouseover", (event) => {
    const selectedJobs = document.getElementsByClassName("job-selected");
    if (!selectedJobs) {
      return;
    }

    for (const job of Array.from(selectedJobs)) {
      job.classList.remove("job-selected");
    }
    (event.currentTarget as HTMLElement).classList.add("job-selected");
  });
  li.addEventListener("mouseout", (event) => {
    (event.currentTarget as HTMLElement).classList.remove("job-selected");
  });
}

function addOptionFuzzySearch(fz: FuzzySearch, data: string[], id: string,
  list: HTMLElement, input: HTMLInputElement,
  stopAutoFill?: boolean): void {
  if (!stopAutoFill) {
    input.value = getParameterByName(id) || '';
  }
  while (list.firstChild) {
    list.removeChild(list.firstChild);
  }
  list.scrollTop = 0;
  for (const datum of data) {
    const li = document.createElement("li");
    li.innerHTML = datum;
    registerJobResultEventHandler(fz, li, input);
    list.appendChild(li);
  }
}

function addOptions(options: string[], selectID: string): string | undefined {
  const sel = document.getElementById(selectID)! as HTMLSelectElement;
  while (sel.length > 1) {
    sel.removeChild(sel.lastChild);
  }
  const param = getParameterByName(selectID);
  for (const option of options) {
    const o = document.createElement("option");
    o.text = option;
    if (param && option === param) {
      o.selected = true;
    }
    sel.appendChild(o);
  }
  return param;
}

function selectionText(sel: HTMLSelectElement): string {
  return sel.selectedIndex === 0 ? "" : sel.options[sel.selectedIndex].text;
}

function equalSelected(sel: string, t: string): boolean {
  return sel === "" || sel === t;
}

function groupKey(build: ProwJob): string {
  const {refs: {repo = "", pulls = [], base_ref = "", base_sha = ""} = {}} = build.spec;
  const pr = pulls.length ? pulls[0].number : 0;
  return `${repo} ${pr} ${genLongRefKey(base_ref, base_sha, pulls)}`;
}

// escapeRegexLiteral ensures the given string is escaped so that it is treated as
// an exact value when used within a RegExp. This is the standard substitution recommended
// by https://developer.mozilla.org/en-US/docs/Web/JavaScript/Guide/Regular_Expressions.
function escapeRegexLiteral(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function redraw(fz: FuzzySearch, pushState = true): void {
  const rerunStatus = getParameterByName("rerun");
  const modal = document.getElementById('rerun')!;
  const modalContent = document.querySelector('.modal-content')!;
  const builds = document.getElementById("builds")!.getElementsByTagName(
    "tbody")[0];
  while (builds.firstChild) {
    builds.removeChild(builds.firstChild);
  }

  const args: string[] = [];

  function getSelection(name: string): string {
    const sel = selectionText(document.getElementById(name) as HTMLSelectElement);
    if (sel && name !== 'repo' && !opts[`${name  }s` as keyof RepoOptions][sel]) {
      return "";
    }
    if (sel !== "") {
      args.push(`${name}=${encodeURIComponent(sel)}`);
    }
    return sel;
  }

  function getSelectionFuzzySearch(id: string, inputId: string): RegExp {
    const input = document.getElementById(inputId) as HTMLInputElement;
    const inputText = input.value;
    if (inputText === "") {
      return new RegExp('');
    }
    if (inputText !== "") {
      args.push(`${id}=${encodeURIComponent(inputText)}`);
    }
    if (inputText !== "" && opts[`${id  }s` as keyof RepoOptions][inputText]) {
      return new RegExp(`^${escapeRegexLiteral(inputText)}$`);
    }
    const expr = inputText.split('*').map(escapeRegexLiteral);
    return new RegExp(`^${expr.join('.*')}$`);
  }

  const repoSel = getSelection("repo");
  const opts = optionsForRepo(repoSel);

  const typeSel = getSelection("type") as ProwJobType;
  const pullSel = getSelection("pull");
  const authorSel = getSelection("author");
  const jobSel = getSelectionFuzzySearch("job", "job-input");
  const stateSel = getSelection("state");
  const clusterSel = getSelection("cluster");

  if (pushState && window.history && window.history.pushState !== undefined) {
    if (args.length > 0) {
      history.pushState(null, "", `/?${  args.join('&')}`);
    } else {
      history.pushState(null, "", "/");
    }
  }
  fz.setDict(Object.keys(opts.jobs));
  redrawOptions(fz, opts);

  let lastKey = '';
  const jobCountMap = new Map() as Map<ProwJobState, number>;
  const jobInterval: [number, number][] = [[3600 * 3, 0], [3600 * 12, 0], [3600 * 48, 0]];
  let currentInterval = 0;
  const jobHistogram = new JobHistogram();
  const now = Date.now() / 1000;
  let totalJob = 0;
  let displayedJob = 0;

  for (let i = 0; i < allBuilds.items.length; i++) {
    const build = allBuilds.items[i];
    const {
      metadata: {
        name: prowJobName = "",
      },
      spec: {
        cluster = "",
        type = "",
        job = "",
        agent = "",
        refs: {repo_link = "", base_sha = "", base_link = "", pulls = [], base_ref = ""} = {},
        pod_spec,
      },
      status: {startTime, completionTime = "", state = "", pod_name, build_id = "", url = ""},
    } = build;

    let buildUrl = url;
    if (url.includes('/view/')) {
      buildUrl = `${window.location.origin}/${url.slice(url.indexOf('/view/') + 1)}`;
    }

    let org = "";
    let repo = "";
    if (build.spec.refs !== undefined) {
      org = build.spec.refs.org;
      repo = build.spec.refs.repo;
    } else if (build.spec.extra_refs !== undefined && build.spec.extra_refs.length > 0 ) {
      org = build.spec.extra_refs[0].org;
      repo = build.spec.extra_refs[0].repo;
    }

    if (!equalSelected(typeSel, type)) {
      continue;
    }
    if (!equalSelected(repoSel, `${org}/${repo}`)) {
      continue;
    }
    if (!equalSelected(stateSel, state)) {
      continue;
    }
    if (!equalSelected(clusterSel, cluster)) {
      continue;
    }
    if (!jobSel.test(job)) {
      continue;
    }

    if (pullSel) {
      if (!pulls.length) {
        continue;
      }

      if (!pulls.some((pull: Pull): boolean => {
        const {number: prNumber} = pull;
        return equalSelected(pullSel, prNumber.toString());
      })) {
        continue;
      }
    }

    if (authorSel) {
      if (!pulls.length) {
        continue;
      }

      if (!pulls.some((pull: Pull): boolean => {
        const {author} = pull;
        return equalSelected(authorSel, author);
      })) {
        continue;
      }
    }

    totalJob++;
    jobCountMap.set(state, (jobCountMap.get(state) || 0) + 1);
    const dashCell = "-";

    // accumulate a count of the percentage of successful jobs over each interval
    const started = Date.parse(startTime) / 1000;
    const finished = Date.parse(completionTime) / 1000;

    const durationSec = completionTime ? finished - started : 0;
    const durationStr = completionTime ? formatDuration(durationSec) : dashCell;

    if (currentInterval >= 0 && (now - started) > jobInterval[currentInterval][0]) {
      const successCount = jobCountMap.get("success") || 0;
      const failureCount = jobCountMap.get("failure") || 0;

      const total = successCount + failureCount;
      if (total > 0) {
        jobInterval[currentInterval][1] = successCount / total;
      } else {
        jobInterval[currentInterval][1] = 0;
      }
      currentInterval++;
      if (currentInterval >= jobInterval.length) {
        currentInterval = -1;
      }
    }

    if (displayedJob >= 500) {
      jobHistogram.add(new JobSample(started, durationSec, state, -1));
      continue;
    } else {
      jobHistogram.add(new JobSample(started, durationSec, state, builds.childElementCount));
    }
    displayedJob++;
    const r = document.createElement("tr");
    // State column
    r.appendChild(cell.state(state));
    // Log column
    r.appendChild(createLogCell(build, buildUrl));
    // Rerun column
    r.appendChild(createRerunCell(modal, modalContent, prowJobName));
    // Abort column
    r.appendChild(createAbortCell(modal, modalContent, job, state, prowJobName));
    // Job Yaml column
    r.appendChild(createViewJobCell(prowJobName));
    // Repository column
    const key = groupKey(build);
    if (key !== lastKey) {
      // This is a different PR or commit than the previous row.
      lastKey = key;
      r.className = "changed";

      if (type === "periodic") {
        r.appendChild(cell.text(dashCell));
      } else {
        let repoLink = repo_link;
        if (!repoLink) {
          repoLink = `/github-link?dest=${org}/${repo}`;
        }
        r.appendChild(cell.link(`${org}/${repo}`, repoLink));
      }
      if (type === "presubmit") {
        if (pulls.length) {
          r.appendChild(cell.prRevision(`${org}/${repo}`, pulls[0]));
        } else {
          r.appendChild(cell.text(dashCell));
        }
      } else if (type === "batch") {
        r.appendChild(batchRevisionCell(build));
      } else if (type === "postsubmit") {
        r.appendChild(cell.commitRevision(`${org}/${repo}`, base_ref, base_sha, base_link));
      } else if (type === "periodic") {
        r.appendChild(cell.text(dashCell));
      }
    } else {
      // Don't render identical cells for the same PR/commit.
      r.appendChild(cell.text(dashCell));
      r.appendChild(cell.text(dashCell));
    }
    if (spyglass) {
      // this logic exists for legacy jobs that are configured for gubernator compatibility
      const buildIndex = buildUrl.indexOf('/build/');
      if (buildIndex !== -1) {
        const gcsUrl = `${window.location.origin}/view/gcs/${buildUrl.substring(buildIndex + '/build/'.length)}`;
        r.appendChild(createSpyglassCell(gcsUrl));
      } else if (buildUrl.includes('/view/')) {
        r.appendChild(createSpyglassCell(buildUrl));
      } else {
        r.appendChild(cell.text(''));
      }
    } else {
      r.appendChild(cell.text(''));
    }
    // Results column
    if (buildUrl === "") {
      r.appendChild(cell.text(job));
    } else {
      r.appendChild(cell.link(job, buildUrl));
    }
    // Started column
    r.appendChild(cell.time(i.toString(), moment.unix(started)));
    // Duration column
    r.appendChild(cell.text(durationStr));
    builds.appendChild(r);
  }

  // fill out the remaining intervals if necessary
  if (currentInterval !== -1) {
    let successCount = jobCountMap.get("success");
    if (!successCount) {
      successCount = 0;
    }
    let failureCount = jobCountMap.get("failure");
    if (!failureCount) {
      failureCount = 0;
    }
    const total = successCount + failureCount;
    for (let i = currentInterval; i < jobInterval.length; i++) {
      if (total > 0) {
        jobInterval[i][1] = successCount / total;
      } else {
        jobInterval[i][1] = 0;
      }
    }
  }

  const jobSummary = document.getElementById("job-histogram-summary")!;
  const success = jobInterval.map((interval) => {
    if (interval[1] < 0.5) {
      return `${formatDuration(interval[0])}: <span class="state failure">${Math.ceil(interval[1] * 100)}%</span>`;
    }
    return `${formatDuration(interval[0])}: <span class="state success">${Math.ceil(interval[1] * 100)}%</span>`;
  }).join(", ");
  jobSummary.innerHTML = `Success rate over time: ${success}`;
  const jobCount = document.getElementById("job-count")!;
  jobCount.textContent = `Showing ${displayedJob}/${totalJob} jobs`;
  drawJobBar(totalJob, jobCountMap);

  // if we aren't filtering the output, cap the histogram y axis to 2 hours because it
  // contains the bulk of our jobs
  let max = Number.MAX_SAFE_INTEGER;
  if (totalJob === allBuilds.items.length) {
    max = 2 * 3600;
  }
  drawJobHistogram(totalJob, jobHistogram, now - (12 * 3600), now, max);
  if (rerunStatus === "gh_redirect") {
    modal.style.display = "block";
    modalContent.innerHTML = "Rerunning that job requires GitHub login. Now that you're logged in, try again";
  }
  // we need to upgrade DOM for new created dynamic elements
  // see https://getmdl.io/started/index.html#dynamic
  componentHandler.upgradeDom();
}

function createAbortCell(modal: HTMLElement, modalContent: Element, job: string, state: ProwJobState, prowjob: string): HTMLTableCellElement {
  const c = document.createElement("td");
  c.appendChild(createAbortProwJobIcon(modal, modalContent, job, state, prowjob, csrfToken));
  return c;
}

function createRerunCell(modal: HTMLElement, rerunElement: Element, prowjob: string): HTMLTableDataCellElement {
  const c = document.createElement("td");
  c.appendChild(createRerunProwJobIcon(modal, rerunElement, prowjob, rerunCreatesJob, csrfToken));
  return c;
}

function createLogCell(build: ProwJob, buildUrl: string): HTMLTableDataCellElement {
  const { agent, job, pod_spec } = build.spec;
  const { pod_name, build_id } = build.status;

  if ((agent === "kubernetes" && pod_name) || agent !== "kubernetes") {
    const logIcon = icon.create("description", "Build log");
    if (pod_spec == null || pod_spec.containers.length <= 1) {
      logIcon.href = `log?job=${job}&id=${build_id}`;
    } else {
      // this logic exists for legacy jobs that are configured for gubernator compatibility
      const buildIndex = buildUrl.indexOf('/build/');
      if (buildIndex !== -1) {
        const gcsUrl = `${window.location.origin}/view/gcs/${buildUrl.substring(buildIndex + '/build/'.length)}`;
        logIcon.href = gcsUrl;
      } else if (buildUrl.includes('/view/')) {
        logIcon.href = buildUrl;
      } else {
        logIcon.href = `log?job=${job}&id=${build_id}`;
      }
    }
    const c = document.createElement("td");
    c.appendChild(logIcon);
    return c;
  }
  return cell.text("");
}

function createViewJobCell(prowjob: string): HTMLTableDataCellElement {
  const c = document.createElement("td");
  const i = icon.create("pageview", "Show job YAML", () => gtag("event", "view_job_yaml", {event_category: "engagement", transport_type: "beacon"}));
  i.href = `/prowjob?prowjob=${prowjob}`;
  c.appendChild(i);
  return c;
}

function batchRevisionCell(build: ProwJob): HTMLTableDataCellElement {
  const {refs: {org = "", repo = "", pulls = []} = {}} = build.spec;

  const c = document.createElement("td");
  if (!pulls.length) {
    return c;
  }
  for (let i = 0; i < pulls.length; i++) {
    if (i !== 0) {
      c.appendChild(document.createElement("br"));
    }
    cell.addPRRevision(c, `${org}/${repo}`, pulls[i]);
  }
  return c;
}

function drawJobBar(total: number, jobCountMap: Map<ProwJobState, number>): void {
  const states: ProwJobState[] = ["success", "pending", "triggered", "error", "failure", "aborted", ""];
  states.sort((s1, s2) => {
    return jobCountMap.get(s1)! - jobCountMap.get(s2)!;
  });
  states.forEach((state, index) => {
    const count = jobCountMap.get(state);
    // If state is undefined or empty, treats it as unknown state.
    if (!state) {
      state = "unknown";
    }
    const id = `job-bar-${  state}`;
    const el = document.getElementById(id)!;
    const tt = document.getElementById(`${state  }-tooltip`)!;
    if (!count || count === 0 || total === 0) {
      el.textContent = "";
      tt.textContent = "";
      el.style.width = "0";
    } else {
      el.textContent = count.toString();
      tt.textContent = `${count} ${stateToAdj(state)} jobs`;
      if (index === states.length - 1) {
        el.style.width = "auto";
      } else {
        el.style.width = `${Math.max((count / total * 100), 1)  }%`;
      }
    }
  });
}

function stateToAdj(state: ProwJobState): string {
  switch (state) {
    case "success":
      return "succeeded";
    case "failure":
      return "failed";
    default:
      return state;
  }
}

function drawJobHistogram(total: number, jobHistogram: JobHistogram, start: number, end: number, maximum: number): void {
  const startEl = document.getElementById("job-histogram-start") as HTMLSpanElement;
  if (startEl != null) {
    startEl.textContent = `${formatDuration(end - start)} ago`;
  }

  // make sure the empty table is hidden
  const tableEl = document.getElementById("job-histogram") as HTMLTableElement;
  const labelsEl = document.getElementById("job-histogram-labels") as HTMLDivElement;
  if (jobHistogram.length === 0) {
    tableEl.style.display = "none";
    labelsEl.style.display = "none";
    return;
  }
  tableEl.style.display = "";
  labelsEl.style.display = "";

  const el = document.getElementById("job-histogram-content") as HTMLTableSectionElement;
  el.title = `Showing ${jobHistogram.length} builds from last ${formatDuration(end - start)} by start time and duration, newest to oldest.`;
  const rows = 10;
  const width = 12;
  const cols = Math.round(el.clientWidth / width);

  // initialize the table if the row count changes
  if (el.childNodes.length !== rows) {
    el.innerHTML = "";
    for (let i = 0; i < rows; i++) {
      const tr = document.createElement('tr');
      for (let j = 0; j < cols; j++) {
        const td = document.createElement('td');
        tr.appendChild(td);
      }
      el.appendChild(tr);
    }
  }

  const buckets = jobHistogram.buckets(start, end, cols);
  buckets.limitMaximum(maximum);

  // show the max and mid y-axis labels rounded up to the nearest 10 minute mark
  let maxY = buckets.max;
  maxY = Math.ceil(maxY / 600);
  const yMax = document.getElementById("job-histogram-labels-y-max") as HTMLSpanElement;
  yMax.innerText = `${formatDuration(maxY * 600)}+`;
  const yMid = document.getElementById("job-histogram-labels-y-mid") as HTMLSpanElement;
  yMid.innerText = `${formatDuration(maxY / 2 * 600)}`;

  // populate the buckets
  buckets.data.forEach((bucket, colIndex) => {
    let lastRowIndex = 0;
    buckets.linearChunks(bucket, rows).forEach((samples, rowIndex) =>  {
      lastRowIndex = rowIndex + 1;
      const td = el.childNodes[rows - 1 - rowIndex].childNodes[cols - colIndex - 1] as HTMLTableCellElement;
      if (samples.length === 0) {
        td.removeAttribute('title');
        td.className = '';
        return;
      }
      td.dataset.sampleRow = String(samples[0].row);
      const failures = samples.reduce((sum, sample) => {
        return sample.state !== 'success' ? sum + 1 : sum;
      }, 0);
      if (failures === 0) {
        td.title = `${samples.length} succeeded`;
      } else {
        if (failures === samples.length) {
          td.title = `${failures} failed`;
        } else {
          td.title = `${failures}/${samples.length} failed`;
        }
      }
      td.style.opacity = String(0.2 + samples.length / bucket.length * 0.8);
      if (samples[0].row !== -1) {
        td.className = `active success-${Math.floor(10 - (failures / samples.length) * 10)}`;
      } else {
        td.className = `success-${Math.floor(10 - (failures / samples.length) * 10)}`;
      }
    });
    for (let rowIndex = lastRowIndex; rowIndex < rows; rowIndex++) {
      const td = el.childNodes[rows - 1 - rowIndex].childNodes[cols - colIndex - 1] as HTMLTableCellElement;
      td.removeAttribute('title');
      td.className = '';
    }
  });
}

function createSpyglassCell(url: string): HTMLTableDataCellElement {
  const i = icon.create('visibility', 'View in Spyglass');
  i.href = url;
  const c = document.createElement('td');
  c.appendChild(i);
  return c;
}
