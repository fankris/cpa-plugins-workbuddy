export const workspaceIDs=['dashboard','accounts','models','tasks','settings'] as const;
export type Tab=typeof workspaceIDs[number];
// Migrate both old deep links and the same-origin host's transient view state.
export function workspaceTab(value:unknown):Tab {
 if(value==='results')return 'dashboard';
 return workspaceIDs.includes(value as Tab)?value as Tab:'accounts';
}
