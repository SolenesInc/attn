import './SharedConversationNotice.css';

interface SharedConversationNoticeProps {
  openIn: string[];
}

export function SharedConversationNotice({ openIn }: SharedConversationNoticeProps) {
  return (
    <div className="shared-conversation-notice" role="status" data-testid="shared-conversation-notice">
      <p className="shared-conversation-notice-lead">This conversation is also open in {openIn.join(', ')}.</p>
      <p>Two Claude processes on one conversation split its history. Close one of them.</p>
    </div>
  );
}
