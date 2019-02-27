import {isResponse, isTransitMessage, Message, Response} from './common';

export interface Spyglass {
  /**
   * Replaces the lens display with a new server-rendered page.
   * The returned promise will be resolved once the page has been updated.
   *
   * @param data Some data to pass back to the server. JSON encoding is
   *             recommended, but not required.
   */
  updatePage(data: string): Promise<void>;
  /**
   * Requests that the server re-render the lens with the provided data, and
   * returns a promise that will resolve with that HTML as a string.
   *
   * This is equivalent to updatePage(), except that the displayed content is
   * not automatically changed.
   * @param data Some data to pass back to the server. JSON encoding is
   *             recommended, but not required.
   */
  requestPage(data: string): Promise<string>;
  /**
   * Sends a request to the server-side lens backend with the provided data, and
   * returns a promise that will resolve with the response as a string.
   *
   * @param data Some data to pass back to the server. JSON encoding is
   *             recommended, but not required.
   */
  request(data: string): Promise<string>;
  /**
   * Inform Spyglass that the lens content has updated. This should be called whenever
   * the visible content changes, so Spyglass can ensure that all content is visible.
   */
  contentUpdated(): void;
}

class SpyglassImpl implements Spyglass {
  private pendingRequests = new Map<number, (v: Response) => void>();
  private messageId = 0;

  constructor() {
    window.addEventListener('message', (e) => this.handleMessage(e));
  }

  public async updatePage(data: string): Promise<void> {
    await this.postMessage({type: 'updatePage', data});
    this.contentUpdated();
  }
  public async requestPage(data: string): Promise<string> {
    const result = await this.postMessage({type: 'requestPage', data});
    return result.data;
  }
  public async request(data: string): Promise<string> {
    const result = await this.postMessage({type: 'request', data});
    return result.data;
  }
  public contentUpdated(): void {
    // Use .then() instead of await to avoid infecting our caller with our
    // asynchronicity.
    this.postMessage({type: 'contentUpdated', height: document.body.offsetHeight}).then(({data}) => {
      // If before this call we were not actually visible, recalculate our height.
      // This works around a bizarre issue where other elements on the parent page
      // being resized can cause us to produce an incorrect height. Recalculating
      // after we become visible ensures we produce the correct value.
      if (data === 'madeVisible') {
        this.contentUpdated();
      }
    });
  }

  private postMessage(message: Message): Promise<Response> {
    return new Promise<Response>((resolve, reject) => {
      const id = ++this.messageId;
      this.pendingRequests.set(id, resolve);
      window.parent.postMessage({id, message}, document.location.origin);
    });
  }

  private handleMessage(e: MessageEvent) {
    if (e.origin !== document.location.origin) {
      console.warn(`Got MessageEvent from unexpected origin ${e.origin}; expected ${document.location.origin}`, e);
      return;
    }
    const data = e.data;
    if (isTransitMessage(data)) {
      if (isResponse(data.message)) {
        if (this.pendingRequests.has(data.id)) {
          this.pendingRequests.get(data.id)!(data.message);
          this.pendingRequests.delete(data.id);
        }
      }
    }
  }
}

const spyglass = new SpyglassImpl();

window.addEventListener('load', () => {
  spyglass.contentUpdated();
});

(window as any).spyglass = spyglass;
