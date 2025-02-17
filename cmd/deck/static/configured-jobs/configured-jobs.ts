import "code-prettify";
import dialogPolyfill from "dialog-polyfill";
import {Prettify} from "../common/prettify";
import {getParameterByName} from "../common/urls";

declare const PR: Prettify;
declare const includedRepos: Repo[];
declare const allRepos: string[];

window.onload = (): void => {
    redraw();
    // Register dialog
    const dialog = document.querySelector('dialog');
    dialogPolyfill.registerDialog(dialog);
    dialog.querySelector('.close')!.addEventListener('click', () => {
        dialog.close();
    });
}

interface Repo {
    org: Org;
    safeName: string;
    name: string;
    jobs: Job[];
}

interface Org {
    name: string
}

interface Job {
    name: string;
    type: string;
    yamlDefinition: string;
    jobHistoryLink: string;
}

/**
 * Redraws the content of the page.
 */
function redraw(): void {
    redrawOptions();
    includedRepos.forEach((repo) => {
        redrawRepo(repo);
    })
}

/**
 * Redraws the repo selection box
 */
function redrawOptions(): void {
    const repos = allRepos.sort();
    const sel = document.getElementById("repo") as HTMLSelectElement;
    while (sel.length > 1) {
        sel.removeChild(sel.lastChild);
    }

    //If we are only showing a single repo, we should have that option as selected
    let selectedRepo: string
    if (includedRepos.length == 1) {
        selectedRepo = includedRepos[0].org.name + "/" + includedRepos[0].name
    }

    repos.forEach((repo) => {
        const o = document.createElement("option");
        o.text = repo;
        o.value = "/configured-jobs/" + repo;
        o.selected = repo === selectedRepo
        sel.appendChild(o);
    });

    sel.addEventListener("change", (e) => {
        window.location.href = sel.value;
    })
}

/**
 * Redraws the content of the provided repo.
 */
function redrawRepo(repo: Repo): void {
    const container = document.querySelector("#job-container")!;
    const repoContainer = container.querySelector(`#job-container-${repo.safeName}`)!;
    while (repoContainer.childElementCount !== 0) {
        repoContainer.removeChild(repoContainer.firstChild);
    }

    if (repo.jobs.length > 0) {
        repo.jobs.forEach((job) => {
            repoContainer.appendChild(createJobCard(job));
        });
    } else {
        const message = document.createElement("h3");
        message.innerText = "No Jobs found for " + repo.org.name + "/" + repo;
        repoContainer.appendChild(message);
    }
}

/**
 * Creates and returns a card for the provided job
 */
function createJobCard(job: Job): HTMLElement {
    const title = document.createElement("h3")
    title.innerText = job.name;
    title.classList.add("mdl-card__title-text");
    const cardTitle = document.createElement("div");
    cardTitle.classList.add("mdl-card__title");
    cardTitle.appendChild(title);

    const cardDesc = document.createElement("div");
    cardDesc.innerText = job.type;
    cardDesc.classList.add("mdl-card__supporting-text");

    const cardAction = document.createElement("div");
    const actionButton = document.createElement("a");
    actionButton.innerText = "Details";
    actionButton.classList.add(...["mdl-button", "mdl-button--colored", "mdl-js-button", "mdl-js-ripple-effect"]);
    actionButton.addEventListener("click", () => {
        const dialogElement = document.querySelector("dialog");
        const titleElement = <HTMLHeadingElement>dialogElement.querySelector("#job-title")!;
        titleElement.innerText = job.name;
        const contentElement = dialogElement.querySelector(".mdl-dialog__content")!;

        while (contentElement.firstChild) {
            contentElement.removeChild(contentElement.firstChild);
        }

        const container = document.createElement("div");
        const sectionTitle = document.createElement("h5");
        const sectionBody = document.createElement("div");
        contentElement.appendChild(container);

        sectionBody.classList.add("dialog-section-body");
        sectionBody.appendChild(genJobDetails(job));
        sectionTitle.classList.add("dialog-section-title");
        sectionTitle.innerText = "Job Definition";

        container.classList.add("dialog-section");
        container.appendChild(sectionTitle);
        container.appendChild(sectionBody);
        PR.prettyPrint();
        dialogElement.showModal();
    });
    cardAction.appendChild(actionButton);
    cardAction.classList.add("mdl-card__actions", "mdl-card--border");

    const card = document.createElement("div");
    card.appendChild(cardTitle);
    card.appendChild(cardDesc);
    card.appendChild(cardAction);
    card.classList.add("job-definition-card", "mdl-card", "mdl-shadow--2dp");

    return card;
}

/**
 * Creates and returns the inner content of the modal display for the provided job
 */
function genJobDetails(job: Job): HTMLDivElement {
    const jobHistoryLink = document.createElement('a');
    jobHistoryLink.href = job.jobHistoryLink;
    jobHistoryLink.appendChild(document.createTextNode('Job History'));

    const code = document.createElement('code');
    code.className = 'language-yaml job-definition';
    code.appendChild(document.createTextNode(job.yamlDefinition));

    const jobDefinition = document.createElement('pre');
    jobDefinition.className = 'prettyprint';
    jobDefinition.appendChild(code);

    const jobType = document.createElement('summary');
    jobType.appendChild(document.createTextNode(`Type: ${job.type}`));

    const jobDetails = document.createElement('div');
    jobDetails.appendChild(jobType);
    jobDetails.appendChild(jobDefinition);
    jobDetails.appendChild(jobHistoryLink);

    return jobDetails;
}
