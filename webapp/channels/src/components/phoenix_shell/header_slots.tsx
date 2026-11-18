// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.

import React, {createContext, useCallback, useContext, useLayoutEffect, useMemo, useRef, useState} from 'react';
import type { JSX, ReactNode} from 'react';
import {createPortal} from 'react-dom';

export type HeaderSlot = 'left' | 'channel' | 'utilities';

type HeaderSlotsContextValue = {
    active: boolean;
    containers: Record<HeaderSlot, HTMLDivElement>;
    counts: Record<HeaderSlot, number>;
    mobileChannelCount: number;
    registerContent: (name: HeaderSlot) => () => void;
    registerMobileChannel: () => () => void;
};

const HeaderSlotsContext = createContext<HeaderSlotsContextValue | null>(null);
const ActiveSlotContext = createContext<HeaderSlot | null>(null);

function createSlotContainers(): Record<HeaderSlot, HTMLDivElement> {
    const make = (slot: HeaderSlot) => {
        const el = document.createElement('div');
        el.className = `phoenix-shell__slot-content phoenix-shell__slot-content--${slot}`;
        return el;
    };
    return {
        left: make('left'),
        channel: make('channel'),
        utilities: make('utilities'),
    };
}

export function HeaderSlotsProvider({
    children,
    active = true,
}: {
    children: ReactNode;
    active?: boolean;
}): JSX.Element {
    const [containers] = useState<Record<HeaderSlot, HTMLDivElement>>(createSlotContainers);
    const [counts, setCounts] = useState<Record<HeaderSlot, number>>({left: 0, channel: 0, utilities: 0});
    const [mobileChannelCount, setMobileChannelCount] = useState(0);

    const registerContent = useCallback((name: HeaderSlot) => {
        setCounts((prev) => ({...prev, [name]: prev[name] + 1}));
        return () => {
            setCounts((prev) => ({...prev, [name]: Math.max(0, prev[name] - 1)}));
        };
    }, []);

    const registerMobileChannel = useCallback(() => {
        setMobileChannelCount((prev) => prev + 1);
        return () => {
            setMobileChannelCount((prev) => Math.max(0, prev - 1));
        };
    }, []);

    const value = useMemo<HeaderSlotsContextValue>(() => ({
        active,
        containers,
        counts,
        mobileChannelCount,
        registerContent,
        registerMobileChannel,
    }), [active, containers, counts, mobileChannelCount, registerContent, registerMobileChannel]);

    return (
        <HeaderSlotsContext.Provider value={value}>
            {children}
        </HeaderSlotsContext.Provider>
    );
}

export function HeaderSlotTarget({name}: {name: HeaderSlot}): JSX.Element {
    const ctx = useContext(HeaderSlotsContext);
    const hostRef = useRef<HTMLDivElement | null>(null);

    const setHost = useCallback((host: HTMLDivElement | null) => {
        if (!ctx || !ctx.active) {
            return;
        }
        const el = ctx.containers[name];
        const prev = hostRef.current;
        hostRef.current = host;
        if (host) {
            if (el.parentNode !== host) {
                host.appendChild(el);
            }
        } else if (prev && el.parentNode === prev) {
            prev.removeChild(el);
        }
    }, [ctx, name]);

    return (
        <div
            ref={setHost}
            className={`phoenix-shell__slot phoenix-shell__slot--${name}`}
            data-header-slot={name}
        />
    );
}

export function HeaderSlotContent({
    name,
    children,
    fallback,
}: {
    name: HeaderSlot;
    children: ReactNode;
    fallback?: ReactNode;
}): JSX.Element | null {
    const ctx = useContext(HeaderSlotsContext);
    const activeSlot = useContext(ActiveSlotContext);
    const isNestedSameSlot = Boolean(ctx?.active && activeSlot === name);

    useLayoutEffect(() => {
        if (!ctx?.active || isNestedSameSlot) {
            return undefined;
        }
        return ctx.registerContent(name);
    }, [ctx, name, isNestedSameSlot]);

    if (!ctx || !ctx.active) {
        return <>{fallback !== undefined ? fallback : children}</>;
    }
    if (isNestedSameSlot) {
        return <>{children}</>;
    }
    return createPortal(
        <ActiveSlotContext.Provider value={name}>
            {children}
        </ActiveSlotContext.Provider>,
        ctx.containers[name],
    );
}

export function useHasHeaderSlots(): boolean {
    const ctx = useContext(HeaderSlotsContext);
    return Boolean(ctx?.active);
}

export function useHasSlotContent(name: HeaderSlot): boolean {
    const ctx = useContext(HeaderSlotsContext);
    return Boolean(ctx?.active && ctx.counts[name] > 0);
}

export function useMobileHeaderSlot(register = false): boolean {
    const ctx = useContext(HeaderSlotsContext);
    useLayoutEffect(() => {
        if (!register || !ctx?.active) {
            return undefined;
        }
        return ctx.registerMobileChannel();
    }, [ctx, register]);
    return Boolean(ctx?.active && ctx.mobileChannelCount > 0);
}
