import { useId, useState } from 'react';
import { useEscapeStack } from '../../hooks/useEscapeStack';

function WorkspaceDiagram() {
  return (
    <svg className="mp-explainer-art" viewBox="0 0 420 214" aria-hidden="true">
      <rect className="frame" x="1" y="1" width="418" height="212" rx="10" />
      <line className="frame" x1="124" y1="1" x2="124" y2="213" />

      <rect className="focus" x="9" y="12" width="107" height="80" rx="7" />
      <text className="group" x="19" y="31">exo</text>
      <rect className="row selected" x="17" y="40" width="91" height="20" rx="4" />
      <text className="label" x="25" y="54">Chief</text>
      <rect className="row" x="17" y="64" width="91" height="20" rx="4" />
      <text className="label" x="25" y="78">Tweets</text>

      <text className="group dim" x="19" y="119">attn</text>
      <rect className="row dim" x="17" y="128" width="91" height="20" rx="4" />
      <text className="label dim" x="25" y="142">Review</text>
      <text className="group dim" x="19" y="173">notes</text>
      <rect className="row dim" x="17" y="182" width="91" height="20" rx="4" />
      <text className="label dim" x="25" y="196">Journal</text>

      <rect className="focus" x="133" y="12" width="277" height="190" rx="7" />
      <rect className="pane" x="142" y="21" width="127" height="172" rx="5" />
      <text className="label" x="152" y="39">Chief</text>
      <rect className="ink" x="152" y="52" width="92" height="5" rx="2" />
      <rect className="ink" x="152" y="64" width="70" height="5" rx="2" />
      <rect className="ink" x="152" y="76" width="84" height="5" rx="2" />
      <rect className="pane" x="275" y="21" width="126" height="172" rx="5" />
      <text className="label" x="285" y="39">Tweets</text>
      <rect className="ink" x="285" y="52" width="80" height="5" rx="2" />
      <rect className="ink" x="285" y="64" width="96" height="5" rx="2" />

      <circle className="marker" cx="116" cy="12" r="10" />
      <text className="marker-text" x="116" y="16.5">1</text>
      <circle className="marker" cx="410" cy="12" r="10" />
      <text className="marker-text" x="410" y="16.5">2</text>
    </svg>
  );
}

// Opens on hover and on keyboard focus, so the explainer is reachable without a pointer.
export function WorkspaceExplainer() {
  const [open, setOpen] = useState(false);
  const popupId = useId();
  useEscapeStack(() => setOpen(false), open);
  return (
    <span className="mp-explainer" onMouseEnter={() => setOpen(true)} onMouseLeave={() => setOpen(false)}>
      <button
        type="button"
        className="mp-explainer-trigger"
        aria-describedby={popupId}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onClick={() => setOpen(true)}
      >
        What is a workspace?
      </button>
      <span id={popupId} role="tooltip" className="mp-explainer-popup" hidden={!open}>
        <WorkspaceDiagram />
        <span className="mp-explainer-key">
          <span><b aria-hidden="true">1</b>A group in the sidebar: a folder name with its sessions listed under it.</span>
          <span><b aria-hidden="true">2</b>Everything you see together after clicking it: its sessions, splits and tiles, on one screen.</span>
        </span>
        <span className="mp-explainer-foot">Each workspace with sessions is now one desktop.</span>
      </span>
    </span>
  );
}
