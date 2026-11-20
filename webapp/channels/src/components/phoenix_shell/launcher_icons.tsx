// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {type ReactNode} from 'react';

/**
 * Shell icon allowlist (plan §3.2/§4): `iconKey` values map to a fixed set of
 * inline SVGs. Unknown or missing keys fall back to a generic app tile —
 * the catalog never renders arbitrary markup or external icon URLs.
 *
 * All icons are 24x24 stroke glyphs drawn on currentColor.
 */

function svg(children: ReactNode, viewBox = '0 0 24 24') {
    return (
        <svg
            viewBox={viewBox}
            width='1em'
            height='1em'
            aria-hidden='true'
            focusable='false'
            fill='none'
            stroke='currentColor'
            strokeWidth='1.6'
            strokeLinecap='round'
            strokeLinejoin='round'
        >
            {children}
        </svg>
    );
}

const ICONS: Record<string, ReactNode> = {
    mail: svg(
        <>
            <rect
                x='3.5'
                y='5.5'
                width='17'
                height='13'
                rx='2'
            />
            <path d='m4.5 7.5 7.5 6 7.5-6'/>
        </>,
    ),
    zalo: svg(<><path d='M4 4h16v12H9l-5 4z'/><path d='M8 8h8l-8 5h8'/></>),
    emis: svg(<><path d='M12 4 3 8.5 12 13l9-4.5Z'/><path d='M6.5 10.5v5c0 1.8 2.5 3.5 5.5 3.5s5.5-1.7 5.5-3.5v-5'/><path d='M21 8.5v6'/></>),
    lms: svg(<><path d='M4 5.5A2.5 2.5 0 0 1 6.5 3H20v15H6.5A2.5 2.5 0 0 0 4 20.5Z'/><path d='M4 20.5A2.5 2.5 0 0 1 6.5 18H20'/><path d='M8.5 7.5h7'/></>),

    /** HRM (LK-01): people/ID glyph from the existing stroke set; unknown keys still fall back to `app`. */
    hrm: svg(
        <>
            <rect
                x='3.5'
                y='5.5'
                width='17'
                height='13'
                rx='2'
            />
            <circle
                cx='9'
                cy='11'
                r='2'
            />
            <path d='M5.8 16c.5-1.7 1.7-2.6 3.2-2.6s2.7.9 3.2 2.6'/>
            <path d='M14.5 10h3.5M14.5 13h3'/>
        </>,
    ),
    portal: svg(
        <>
            <path d='M4 11.5 12 4l8 7.5'/>
            <path d='M6.5 10v9h11v-9'/>
            <path d='M10 19v-4.5h4V19'/>
        </>,
    ),
    gateway: svg(
        <>
            <path d='M12 3 5 6v5.5c0 4.2 2.8 7.2 7 9 4.2-1.8 7-4.8 7-9V6z'/>
            <path d='m9 12 2.2 2.2L15.5 10'/>
        </>,
    ),
    tasks: svg(
        <>
            <rect
                x='4'
                y='4'
                width='16'
                height='16'
                rx='2.5'
            />
            <path d='m8.5 12.2 2.4 2.4 4.6-5'/>
        </>,
    ),
    chat: svg(
        <>
            <path d='M4.5 6.5a2.5 2.5 0 0 1 2.5-2.5h10A2.5 2.5 0 0 1 19.5 6.5v7a2.5 2.5 0 0 1-2.5 2.5H11l-4.4 3.4c-.4.3-1-.1-1-.6V6.5Z'/>
            <path d='M8.5 8.5h7M8.5 11.5h4.5'/>
        </>,
    ),
    notes: svg(
        <>
            <path d='M6 4.5h9.5l3 3.5v11.5H6a2 2 0 0 1-2-2v-13Z'/>
            <path d='M15 4.5V8h3.5'/>
            <path d='M9 12h6M9 15h4'/>
        </>,
    ),
    files: svg(
        <>
            <path d='M4 7.5A1.5 1.5 0 0 1 5.5 6h4l2 2.5h7A1.5 1.5 0 0 1 20 10v7.5a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 17.5z'/>
            <path d='M4 11.5h16'/>
        </>,
    ),
    office: svg(
        <>
            <path d='M7 3.5h7l3.5 3.5V20.5H7a2 2 0 0 1-2-2v-13a2 2 0 0 1 2-2Z'/>
            <path d='M13.5 3.5V7H17'/>
            <path d='M9.5 12h5M9.5 15h5'/>
        </>,
    ),
    crm: svg(
        <>
            <circle
                cx='9'
                cy='8.5'
                r='3'
            />
            <path d='M4.5 19.5c.6-3 2.5-5 4.5-5s3.9 2 4.5 5'/>
            <circle
                cx='16.5'
                cy='9.5'
                r='2.2'
            />
            <path d='M15.5 14.6c2.2.1 3.7 1.8 4 4.4'/>
        </>,
    ),
    meet: svg(
        <>
            <rect
                x='4'
                y='7'
                width='11'
                height='10'
                rx='2'
            />
            <path d='m15.5 10.5 4.5-3v9l-4.5-3'/>
        </>,
    ),

    /** Generic fallback for unknown icon keys. */
    app: svg(
        <>
            <rect
                x='5'
                y='5'
                width='14'
                height='14'
                rx='3'
            />
            <circle
                cx='12'
                cy='12'
                r='3'
            />
        </>,
    ),
};

/** Icon for a tile: keyed lookup on the allowlist, generic tile on miss. */
export function launcherIcon(iconKey: string | null | undefined): ReactNode {
    return ICONS[iconKey ?? ''] ?? ICONS.app;
}

