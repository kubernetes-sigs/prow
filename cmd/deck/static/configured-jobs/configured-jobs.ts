import "code-prettify";
import dialogPolyfill from "dialog-polyfill";
import {Prettify} from "../common/prettify";

declare const PR: Prettify;
declare const repos; //TODO: this could be formalized as a type

window.onload = (): void => {
    redraw();
    // Register dialog
    const dialog = document.querySelector('dialog') ;
    dialogPolyfill.registerDialog(dialog);
    dialog.querySelector('.close')!.addEventListener('click', () => {
        dialog.close();
    });
}

interface Job {
    Type: string;
    JobDefinition: string;
}

/**
 * Redraws the content of the page.
 */
function redraw(): void {
    repos.forEach((repo) => {
        if (repo.jobs != null) {
            let jobs: Map<string, Job> = new Map();
            repo.jobs.forEach((job) => {
                jobs.set(job.name, {
                    Type: job.type,
                    JobDefinition: job.yamlDefinition
                })
            })

            redrawRepo(jobs, repo.org, repo.name, repo.safeName)
        }
    })
}

//TODO: document
function redrawRepo(jobs: Map<string, Job>, org: string, repo: string, safeRepoName: string): void {
    console.log("redrawing for " + org + "/" + repo); //TODO: remove prior to check in
    const container = document.querySelector("#job-container")!;
    const repoContainer = container.querySelector(`#job-container-${safeRepoName}`)!;
    while (repoContainer.childElementCount !== 0) {
        repoContainer.removeChild(repoContainer.firstChild);
    }

    if (jobs.size > 0) {
        const names = jobs.keys();
        const namesArray = Array.from(names).sort();
        console.log("namesArray consists of: " + namesArray);
        namesArray.forEach((name) => {
            console.log("creating card for:" + name); //TODO: remove prior to check in
            let job = jobs.get(name)!;
            repoContainer.appendChild(createJobCard(name, job.Type, job.JobDefinition));
        });
    } else {
        const message = document.createElement("h3");
        message.innerHTML = "No Jobs found for " +  org + "/" + repo;
        repoContainer.appendChild(message);
    }
}

//TODO: document
function createJobCard(name: string, type: string, jobYaml: string): HTMLElement {
    const title = document.createElement("h3")
    title.innerHTML = name;
    title.classList.add("mdl-card__title-text");
    const cardTitle = document.createElement("div");
    cardTitle.classList.add("mdl-card__title");
    cardTitle.appendChild(title);

    const cardDesc = document.createElement("div");
    cardDesc.innerHTML = type;
    cardDesc.classList.add("mdl-card__supporting-text");

    const cardAction = document.createElement("div");
    const actionButton = document.createElement("a");
    actionButton.innerHTML = "Details";
    actionButton.classList.add(...["mdl-button", "mdl-button--colored", "mdl-js-button", "mdl-js-ripple-effect"]);
    actionButton.addEventListener("click", () => {
        const dialogElement = document.querySelector("dialog") ;
        const titleElement = dialogElement.querySelector(".mdl-dialog__title")!;
        titleElement.innerHTML = name;
        const contentElement = dialogElement.querySelector(".mdl-dialog__content")!;

        while (contentElement.firstChild) {
            contentElement.removeChild(contentElement.firstChild);
        }

        const container = document.createElement("div");
        const sectionTitle = document.createElement("h5");
        const sectionBody = document.createElement("div");
        contentElement.appendChild(container);

        sectionBody.classList.add("dialog-section-body");
        sectionBody.innerHTML = genJobDetails(type, jobYaml);
        sectionTitle.classList.add("dialog-section-title");
        sectionTitle.innerHTML = "Job Definition";

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

//TODO: document
function genJobDetails(type: string, yamlDefinition: string): string {
    return `
        <div>
            <summary>Type: ${type}</summary>
            <pre class="prettyprint"><code class="language-yaml job-definition">${yamlDefinition}</code></pre>
        </div>
    `;
}
